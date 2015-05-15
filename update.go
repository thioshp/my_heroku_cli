package main

import (
	"crypto/sha1"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dickeyxxx/golock"
	"github.com/franela/goreq"
)

var updateTopic = &Topic{
	Name:        "update",
	Description: "update heroku-cli",
}

var updateCmd = &Command{
	Topic:       "update",
	Hidden:      true,
	Description: "updates heroku-cli",
	Args:        []Arg{{Name: "channel", Optional: true}},
	Run: func(ctx *Context) {
		channel := ctx.Args.(map[string]string)["channel"]
		if channel == "" {
			channel = Channel
		}
		Update(channel)
	},
}

var binPath = filepath.Join(AppDir, "heroku-cli")
var updateLockPath = filepath.Join(AppDir, "updating.lock")
var autoupdateFile = filepath.Join(AppDir, "autoupdate")

func init() {
	if runtime.GOOS == "windows" {
		binPath = binPath + ".exe"
	}
}

// Update updates the CLI and plugins
func Update(channel string) {
	golock.Lock(updateLockPath)
	defer golock.Unlock(updateLockPath)
	done := make(chan bool)
	go func() {
		updateCLI(channel)
		updatePlugins()
		touchAutoupdateFile()
		done <- true
	}()
	select {
	case <-time.After(time.Second * 120):
		Errln("Timed out while updating")
	case <-done:
	}
}

func updatePlugins() {
	Err("updating plugins... ")
	b, _ := node.UpdatePackages()
	Errln("done")
	if len(b) > 0 {
		Err("rebuilding plugins cache... ")
		ClearPluginCache()
		WritePluginCache(GetPlugins())
		Errln("done")
	}
}

func updateCLI(channel string) {
	goreq.SetConnectTimeout(5 * time.Second)
	manifest, err := getUpdateManifest(channel)
	if err != nil {
		Warn("Error updating CLI")
		PrintError(err)
		return
	}
	if manifest.Version == Version && manifest.Channel == Channel {
		return
	}
	if !updatable() {
		Errf("Out of date: You are running %s but %s is out\n", Version, manifest.Version)
		return
	}
	Errf("updating v4 CLI to %s (%s)... ", manifest.Version, manifest.Channel)
	build := manifest.Builds[runtime.GOOS][runtime.GOARCH]
	// on windows we can't remove an existing file or remove the running binary
	// so we download the file to binName.new
	// move the running binary to binName.old (deleting any existing file first)
	// rename the downloaded file to binName
	if err := downloadBin(binPath+".new", build.URL); err != nil {
		panic(err)
	}
	if fileSha1(binPath+".new") != build.Sha1 {
		panic("SHA mismatch")
	}
	os.Remove(binPath + ".old")
	os.Rename(binPath, binPath+".old")
	if err := os.Rename(binPath+".new", binPath); err != nil {
		panic(err)
	}
	os.Remove(binPath + ".old")
	Errln("done")
}

// IsUpdateNeeded checks if an update is available
func IsUpdateNeeded(t string) bool {
	f, err := os.Stat(autoupdateFile)
	if err != nil {
		return true
	}
	if t == "soft" {
		return time.Since(f.ModTime()) > 4*time.Hour
	}
	return time.Since(f.ModTime()) > 168*time.Hour
}

func touchAutoupdateFile() {
	out, err := os.OpenFile(autoupdateFile, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		panic(err)
	}
	out.WriteString(time.Now().String())
}

type manifest struct {
	Channel, Version string
	Builds           map[string]map[string]struct {
		URL, Sha1 string
	}
}

func getUpdateManifest(channel string) (*manifest, error) {
	res, err := goreq.Request{
		Uri: "https://d1gvo455cekpjp.cloudfront.net/" + channel + "/manifest.json",
	}.Do()
	if err != nil {
		return nil, err
	}
	var m manifest
	res.Body.FromJsonTo(&m)
	return &m, nil
}

func updatable() bool {
	path, err := filepath.Abs(os.Args[0])
	if err != nil {
		Errln(err)
	}
	return path == binPath
}

func downloadBin(path, url string) error {
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	res, err := goreq.Request{
		Uri: url + ".gz",
	}.Do()
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, err = io.Copy(out, res.Body)
	if err != nil {
		return err
	}
	return out.Close()
}

func fileSha1(path string) string {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", sha1.Sum(data))
}

// TriggerBackgroundUpdate will trigger an update to the client in the background
func TriggerBackgroundUpdate() {
	exec.Command(binPath, "update").Start()
}

// WarnIfUpdating prints to stderr if the CLI is updating
func WarnIfUpdating() {
	if exists, _ := fileExists(updateLockPath); exists {
		// Disable since v3 calls CLI twice
		//Errln("WARNING: CLI is updating")
	}
}
