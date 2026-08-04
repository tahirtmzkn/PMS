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

// savedConfig is the on-disk form of the app's configuration: the device list
// and the light/dark choice. The counters are not in it — they are a measurement
// of one run, not configuration, so they start at zero on every launch. Nor are
// interval/timeout/interface, which stay session-only.
type savedConfig struct {
	// Theme is "light" or "dark", and absent when following the desktop.
	Theme   string        `json:"theme,omitempty"`
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

// configDirName is the per-app directory under os.UserConfigDir() that holds
// configFileName. legacyConfigDirName is the directory a pre-release build used
// under its working name; it is still read once, by migrateLegacyConfig, so a
// machine that ran that build doesn't silently look like a first run and lose its
// device list. Nothing user-facing carries that name — see CLAUDE.md.
const (
	configDirName       = "pinginfomanager"
	legacyConfigDirName = "pms"
)

func configPathIn(dirName string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, dirName, configFileName), nil
}

func defaultConfigPath() (string, error) { return configPathIn(configDirName) }

func legacyConfigPath() (string, error) { return configPathIn(legacyConfigDirName) }

// migrateLegacyConfig brings a pre-rename device list forward, copying
// legacyPath to path the first time the renamed app runs.
//
// It is keyed on path not *existing*, never on its contents being empty: a user
// who removes every device leaves a valid config with an empty list behind, and
// treating that as "nothing saved yet" would resurrect the old list on the next
// launch. Once the new file is there, for any reason, the old one is never
// looked at again — it is left on disk rather than deleted, so a downgrade to
// the previous package still finds its list.
//
// A missing legacy file is a genuine first run and not an error.
func migrateLegacyConfig(path, legacyPath string) error {
	if path == "" || legacyPath == "" || path == legacyPath {
		return nil
	}

	switch _, err := os.Stat(path); {
	case err == nil:
		return nil // already migrated, or a list this app has since written
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}

	switch _, err := os.Stat(legacyPath); {
	case errors.Is(err, fs.ErrNotExist):
		return nil // nothing to bring forward
	case err != nil:
		return err
	}

	// Round-tripped through loadConfig/saveConfig rather than copied byte for
	// byte, so a corrupt old file is reported here instead of being carried
	// over to fail again under the new name.
	cfg, err := loadConfig(legacyPath)
	if err != nil {
		return err
	}
	return saveConfig(path, cfg)
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

// persistConfig writes the current configuration out. Called after every change
// to it — add, remove, sort, drag, and picking a theme — rather than only on
// exit, so it survives a crash or a kill as well as a clean close, and so no
// window-close hook has to be relied on.
//
// A failed write is logged rather than raised: the app is perfectly usable
// without a config file, and a dialog interrupting every Add would be a worse
// outcome than a list that isn't saved.
func (pm *appState) persistConfig() {
	if pm.configFile == "" {
		return // persistence disabled (no config dir, or a test)
	}

	cfg := savedConfig{
		Theme:   pm.themeMode.configValue(),
		Devices: make([]savedDevice, len(pm.devices)),
	}
	for i, d := range pm.devices {
		cfg.Devices[i] = savedDevice{IP: d.IP, Name: d.Name}
	}
	if err := saveConfig(pm.configFile, cfg); err != nil {
		fyne.LogError("could not save the configuration to "+pm.configFile, err)
	}
}

// loadSavedConfig reads the config file once, for main.go to apply in two parts:
// the theme before the window content is built, so the first paint is already in
// the right palette, and the device list after it. An unreadable file is logged
// and comes back empty — and, importantly, is left on disk rather than
// overwritten, so the next change to the list is what replaces it.
func (pm *appState) loadSavedConfig() savedConfig {
	if pm.configFile == "" {
		return savedConfig{}
	}

	cfg, err := loadConfig(pm.configFile)
	if err != nil {
		fyne.LogError("could not read the saved configuration from "+pm.configFile, err)
		return savedConfig{}
	}
	return cfg
}

// restoreDevices puts a saved list back in the table, in the saved order, and
// starts a hostname lookup for each device exactly as if it had just been added
// — so the Hostname column shows what the device says now, and a device that has
// gone away reads "Empty" rather than showing a remembered name.
//
// The returned channels close as each lookup's result lands; the app ignores
// them, tests wait on them.
func (pm *appState) restoreDevices(saved []savedDevice) []<-chan struct{} {
	restored := make([]*Device, 0, len(saved))
	for _, entry := range saved {
		ip := strings.TrimSpace(entry.IP)
		if ip == "" {
			continue // hand-edited file, or a device saved before this field existed
		}
		name := strings.TrimSpace(entry.Name)
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
