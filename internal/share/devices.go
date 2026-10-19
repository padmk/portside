package share

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/padmk/portside/internal/signing"
)

// Pairing: Tailscale proves which device a request comes from, not which
// account or process on it sent it. So a device must also sign each request
// with a key that this daemon issued to it in exchange for a one-time code
// shown here (see sign.go). The key never travels again after pairing, so a
// listener posing as this daemon learns nothing it could reuse.

// Device is a paired device.
type Device struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	DNSName  string    `json:"dnsName"`
	OS       string    `json:"os,omitempty"`
	PairedAt time.Time `json:"pairedAt"`
	// Instance tells apart daemons on the same device (accounts, a
	// development daemon); each pairs on its own.
	Instance string `json:"instance,omitempty"`
	// Key is the device's signing key, hex encoded; never sent to clients.
	Key string `json:"key,omitempty"`
}

// Code is a pairing code shown to the user.
type Code struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

const (
	codeTTL      = 10 * time.Minute
	codeAttempts = 10 // wrong guesses before the current code is withdrawn
	// Unambiguous characters: no 0/O, 1/I.
	codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// ErrPairing is returned for a wrong, used or expired code.
var ErrPairing = errors.New("the pairing code is wrong or has expired; get a new one on the shared computer")

type devices struct {
	path string

	mu       sync.Mutex
	list     []Device
	code     string
	expires  time.Time
	failures int
}

func newDevices(stateDir string) *devices {
	d := &devices{path: filepath.Join(stateDir, "share-devices.json")}
	if b, err := os.ReadFile(d.path); err == nil {
		_ = json.Unmarshal(b, &d.list)
	}
	return d
}

// saveLocked writes list and makes it the live list only once it is on disk,
// so a failed write changes nothing and can be retried.
func (d *devices) saveLocked(list []Device) error {
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, d.path); err != nil {
		return err
	}
	d.list = list
	return nil
}

// List returns the paired devices without their hashes.
func (d *devices) List() []Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Device, len(d.list))
	for i, dev := range d.list {
		dev.Key = ""
		out[i] = dev
	}
	return out
}

// NewCode replaces any current code with a fresh one.
func (d *devices) NewCode() (Code, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return Code{}, err
	}
	var sb strings.Builder
	for _, x := range b {
		sb.WriteByte(codeAlphabet[int(x)%len(codeAlphabet)])
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.code, d.expires, d.failures = sb.String(), time.Now().Add(codeTTL), 0
	return Code{Code: d.code[:4] + "-" + d.code[4:], ExpiresAt: d.expires}, nil
}

// normalizeCode drops separators and case, so "abcd efgh" matches ABCD-EFGH.
func normalizeCode(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(s) {
		if strings.ContainsRune(codeAlphabet, r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// Pair redeems code for node and returns the device ID and signing key.
func (d *devices) Pair(code string, node Device) (id, key string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	given := normalizeCode(code)
	if d.code == "" || time.Now().After(d.expires) || subtle.ConstantTimeCompare([]byte(given), []byte(d.code)) != 1 {
		if d.code != "" {
			d.failures++
			if d.failures >= codeAttempts {
				d.code = ""
			}
		}
		return "", "", ErrPairing
	}
	d.code = ""
	kb := make([]byte, 32)
	if _, err := rand.Read(kb); err != nil {
		return "", "", err
	}
	ib := make([]byte, 6)
	if _, err := rand.Read(ib); err != nil {
		return "", "", err
	}
	node.ID = hex.EncodeToString(ib)
	node.Key = hex.EncodeToString(kb)
	node.PairedAt = time.Now().UTC()
	// One pairing per daemon instance: pairing again replaces its old key.
	next := []Device{}
	for _, dev := range d.list {
		if !strings.EqualFold(dev.DNSName, node.DNSName) || dev.Instance != node.Instance {
			next = append(next, dev)
		}
	}
	if err := d.saveLocked(append(next, node)); err != nil {
		return "", "", err
	}
	return node.ID, node.Key, nil
}

// ErrAmbiguous is returned when a name matches several paired devices.
var ErrAmbiguous = errors.New("several paired devices have that name; use the device ID or full DNS name")

// Revoke removes a paired device by ID, DNS name or, when unique, host name.
func (d *devices) Revoke(ref string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	match := func(dev Device) bool { return dev.ID == ref || strings.EqualFold(dev.DNSName, ref) }
	exact := 0
	byName := 0
	for _, dev := range d.list {
		if match(dev) {
			exact++
		} else if strings.EqualFold(dev.Name, ref) {
			byName++
		}
	}
	if exact == 0 {
		if byName > 1 {
			return false, ErrAmbiguous
		}
		match = func(dev Device) bool { return strings.EqualFold(dev.Name, ref) }
	}
	next := []Device{}
	found := false
	for _, dev := range d.list {
		if match(dev) {
			found = true
			continue
		}
		next = append(next, dev)
	}
	if !found {
		return false, nil
	}
	return true, d.saveLocked(next)
}

// Lookup returns the paired device with id.
func (d *devices) Lookup(id string) (Device, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, dev := range d.list {
		if dev.ID == id && id != "" && signing.ValidKey(dev.Key) {
			return dev, true
		}
	}
	return Device{}, false
}
