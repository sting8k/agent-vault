package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	fileVersion = 1
	vaultFile   = "vault.json"
	keyFile     = "master.key"
	lockFile    = ".lock"
	keySize     = 32
)

// file is the vault.json document. Field values are ciphertext.
type file struct {
	Version int               `json:"version"`
	KeyID   string            `json:"key_id"`
	Entries map[string]*entry `json:"entries"`
}

type entry struct {
	Description string            `json:"description"`
	Type        string            `json:"type"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Fields      map[string]*field `json:"fields"`
}

type field struct {
	Env   string `json:"env,omitempty"`
	File  bool   `json:"file"`
	Value string `json:"value"`
}

func (v *Vault) path(name string) string { return filepath.Join(v.dir, name) }

// load applies the key/vault state table. It returns the parsed vault and the master key.
//
//	master.key  vault.json            result
//	missing     missing               empty vault; with create, the key is made now
//	present     missing               empty vault; with create, it gets the key's key_id
//	missing     present               error: key lost
//	present     present, other key_id error: wrong key
//	any         unreadable or invalid error naming the file; nothing is overwritten
//
// Without create the filesystem is never changed, and key is nil when vault.json is missing.
// create is only used by writers, which hold the lock.
//
// vault.json is read before master.key: a writer makes the key before the vault, so a reader
// that sees the vault always finds the key, and a half-written key is never read by a reader.
func (v *Vault) load(create bool) (*file, []byte, error) {
	f, err := v.readVault()
	if err != nil {
		return nil, nil, err
	}
	if f != nil {
		key, err := v.readKey()
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("%s is missing, so the entries in %s cannot be decrypted; restore the key",
				v.path(keyFile), v.path(vaultFile))
		}
		if err != nil {
			return nil, nil, err
		}
		if keyID(key) != f.KeyID {
			return nil, nil, fmt.Errorf("%s is the wrong key for %s; restore the key that belongs to this vault",
				v.path(keyFile), v.path(vaultFile))
		}
		return f, key, nil
	}
	empty := &file{Version: fileVersion, Entries: map[string]*entry{}}
	if !create {
		return empty, nil, nil
	}
	key, err := v.readKey()
	if errors.Is(err, fs.ErrNotExist) {
		key, err = v.createKey()
	}
	if err != nil {
		return nil, nil, err
	}
	empty.KeyID = keyID(key)
	return empty, key, nil
}

// readVault returns nil, nil when vault.json does not exist.
func (v *Vault) readVault() (*file, error) {
	p := v.path(vaultFile)
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", p, err)
	}
	f := new(file)
	if err := json.Unmarshal(b, f); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%v); fix or restore it, nothing was changed", p, err)
	}
	if err := f.validate(); err != nil {
		return nil, fmt.Errorf("%s is invalid (%v); fix or restore it, nothing was changed", p, err)
	}
	return f, nil
}

func (v *Vault) readKey() ([]byte, error) {
	p := v.path(keyFile)
	key, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", p, err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("%s must be %d bytes, found %d; restore the key", p, keySize, len(key))
	}
	return key, nil
}

// createKey makes master.key with O_EXCL. The caller holds the lock and has seen no key.
func (v *Vault) createKey() ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	p := v.path(keyFile)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", p, err)
	}
	if _, err = f.Write(key); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = syncDir(v.dir)
	}
	if err != nil {
		os.Remove(p) // a partial key would block the next writer
		return nil, fmt.Errorf("cannot write %s: %w", p, err)
	}
	return key, nil
}

// save replaces vault.json atomically: 0600 temp file, fsync, rename, fsync the directory.
func (v *Vault) save(f *file) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(v.dir, vaultFile+".tmp-*") // CreateTemp makes it 0600
	if err != nil {
		return fmt.Errorf("cannot write %s: %w", v.path(vaultFile), err)
	}
	_, err = tmp.Write(buf.Bytes())
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), v.path(vaultFile))
	}
	if err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("cannot write %s: %w", v.path(vaultFile), err)
	}
	return syncDir(v.dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
