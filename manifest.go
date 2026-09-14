package udb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const recoveryManifestSuffix = ".compact-manifest.json"
const recoveryManifestVersion = 1

type recoveryManifest struct {
	Version      int    `json:"version"`
	Operation    string `json:"operation"`
	SourcePath   string `json:"source_path"`
	TempPath     string `json:"temp_path,omitempty"`
	BackupPath   string `json:"backup_path,omitempty"`
	TempSHA256   string `json:"temp_sha256,omitempty"`
	BackupSHA256 string `json:"backup_sha256,omitempty"`
	KeepBackup   bool   `json:"keep_backup"`
	Stage        string `json:"stage"`
}

func recoveryManifestPath(sourcePath string) string { return sourcePath + recoveryManifestSuffix }

func writeRecoveryManifest(path string, m recoveryManifest) error {
	if m.Version == 0 {
		m.Version = recoveryManifestVersion
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(dir)
}

func removeRecoveryManifest(sourcePath string) {
	path := recoveryManifestPath(sourcePath)
	_ = os.Remove(path)
	_ = os.Remove(path + ".tmp")
	_ = syncDir(filepath.Dir(path))
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, f, make([]byte, 1024*1024)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func validManifestArtifact(path, expectedSHA string, opts Options) bool {
	if !validBoltFile(path, opts) {
		return false
	}
	if expectedSHA == "" {
		return true
	}
	actual, err := fileSHA256(path)
	return err == nil && actual == expectedSHA
}

func readRecoveryManifest(path string) (recoveryManifest, error) {
	data, err := os.ReadFile(recoveryManifestPath(path))
	if err != nil {
		return recoveryManifest{}, err
	}
	var m recoveryManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return recoveryManifest{}, err
	}
	if m.Version != recoveryManifestVersion {
		return recoveryManifest{}, fmt.Errorf("udb: unsupported recovery manifest version %d", m.Version)
	}
	if m.SourcePath != path || m.Operation == "" || m.Stage == "" {
		return recoveryManifest{}, errors.New("udb: invalid recovery manifest identity")
	}
	return m, nil
}
