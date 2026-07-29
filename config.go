package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
)

// savedConfig is the on-disk form of the app's configuration. Only the device
// list is in it: the counters are a measurement of one run, not configuration,
// so they deliberately start at zero on every launch.
type savedConfig struct {
	Devices []savedDevice `json:"devices"`
}

// savedDevice is one device as written to disk — the two things the user chose
// for it. Hostname is not stored: it's what the device answered over SNMP, so
// it's asked again on load rather than restored from a possibly stale file.
type savedDevice struct {
	IP   string `json:"ip"`
	Name string `json:"name"`
}

// configFileName is where the list lives, under os.UserConfigDir()
// (~/.config on Linux). Plain JSON in the standard config directory rather than
// fyne.Preferences, so a list of devices can also be read, edited or copied to
// another machine by hand.
const configFileName = "config.json"

func defaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pms", configFileName), nil
}

// loadConfig reads the config file. A file that isn't there yet is a first run,
// not an error, and comes back as an empty config.
func loadConfig(path string) (savedConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return savedConfig{}, nil
	}
	if err != nil {
		return savedConfig{}, err
	}

	var cfg savedConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return savedConfig{}, err
	}
	return cfg, nil
}

// saveConfig writes cfg to path, creating the directory if needed. It writes a
// temporary file in the same directory and renames it over the target: this app
// gets closed (or killed) at arbitrary moments, and a rename is atomic, so an
// interrupted save can never leave a half-written list where a good one was.
func saveConfig(path string, cfg savedConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, "."+configFileName+".*")
	if err != nil {
		return err
	}
	// Cleans up the temp file on any failure below; a no-op once the rename has
	// moved it away.
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// persistDevices writes the current device list out. Called after every change
// to the list itself — add, remove, sort, drag — rather than only on exit, so
// the list survives a crash or a kill as well as a clean close, and so no
// window-close hook has to be relied on.
//
// A failed write is logged rather than raised: the app is perfectly usable
// without a config file, and a dialog interrupting every Add would be a worse
// outcome than a list that isn't saved.
func (pm *appState) persistDevices() {
	if pm.configFile == "" {
		return // persistence disabled (no config dir, or a test)
	}

	cfg := savedConfig{Devices: make([]savedDevice, len(pm.devices))}
	for i, d := range pm.devices {
		cfg.Devices[i] = savedDevice{IP: d.IP, Name: d.Name}
	}
	if err := saveConfig(pm.configFile, cfg); err != nil {
		fyne.LogError("could not save the device list to "+pm.configFile, err)
	}
}

// restoreDevices puts the saved list back in the table, in the saved order, and
// starts a hostname lookup for each device exactly as if it had just been added
// — so the Hostname column shows what the device says now, and a device that has
// gone away reads "Empty" rather than showing a remembered name.
//
// The returned channels close as each lookup's result lands; the app ignores
// them, tests wait on them. Call it after the window content is built, so the
// rows the lookups write into exist.
func (pm *appState) restoreDevices() []<-chan struct{} {
	if pm.configFile == "" {
		return nil
	}

	cfg, err := loadConfig(pm.configFile)
	if err != nil {
		// A corrupt or unreadable file is left alone rather than overwritten:
		// the next change to the list will replace it, and until then the user
		// still has whatever is in there to look at.
		fyne.LogError("could not read the saved device list from "+pm.configFile, err)
		return nil
	}

	restored := make([]*Device, 0, len(cfg.Devices))
	for _, saved := range cfg.Devices {
		ip := strings.TrimSpace(saved.IP)
		if ip == "" {
			continue // hand-edited file, or a device saved before this field existed
		}
		name := strings.TrimSpace(saved.Name)
		if name == "" {
			name = unknownName
		}
		restored = append(restored, &Device{IP: ip, Name: name, Hostname: resolvingHostname})
	}
	if len(restored) == 0 {
		return nil
	}

	pm.devices = append(pm.devices, restored...)
	pm.refreshRows()

	// Lookups start only once every row exists, so a fast answer can't land in
	// the middle of the rebuild above.
	done := make([]<-chan struct{}, 0, len(restored))
	for _, device := range restored {
		done = append(done, pm.startHostnameLookup(device))
	}
	return done
}
