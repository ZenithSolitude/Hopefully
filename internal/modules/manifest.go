package modules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)

type Manifest struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Author      string            `json:"author"`
	License     string            `json:"license"`
	Repository  string            `json:"repository"`
	Entrypoint  string            `json:"entrypoint"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	Port        int               `json:"port"`
	Requires    []string          `json:"requires"`
	Menu        MenuItem          `json:"menu"`
}

type MenuItem struct {
	Label    string `json:"label"`
	Icon     string `json:"icon"`
	Position int    `json:"position"`
	Hidden   bool   `json:"hidden"`
}

func loadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("manifest.json not found in %s", dir)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid manifest.json: %w", err)
	}
	if !nameRe.MatchString(m.Name) {
		return nil, fmt.Errorf("name must match %s, got %q", nameRe, m.Name)
	}
	if m.Version == "" {
		return nil, fmt.Errorf("version is required")
	}
	return &m, nil
}

func findManifestDir(root string) (string, error) {
	if _, err := os.Stat(filepath.Join(root, "manifest.json")); err == nil {
		return root, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			sub := filepath.Join(root, e.Name())
			if _, err := os.Stat(filepath.Join(sub, "manifest.json")); err == nil {
				return sub, nil
			}
		}
	}
	return "", fmt.Errorf("manifest.json not found")
}
