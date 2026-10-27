package share

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/padmk/portside/internal/signing"
)

var codeFormat = regexp.MustCompile(`^[` + codeAlphabet + `]{4}-[` + codeAlphabet + `]{4}$`)

func newCode(t *testing.T, d *devices) string {
	t.Helper()
	c, err := d.NewCode()
	if err != nil {
		t.Fatal(err)
	}
	if !codeFormat.MatchString(c.Code) {
		t.Fatalf("code %q is not XXXX-XXXX from the code alphabet", c.Code)
	}
	if time.Until(c.ExpiresAt) > codeTTL || time.Until(c.ExpiresAt) < codeTTL-time.Minute {
		t.Fatalf("code expires at %v", c.ExpiresAt)
	}
	return c.Code
}

var mbp = Device{Name: "MBP", DNSName: "mbp.t.ts.net", OS: "macOS"}

func TestPairOnceWithNormalizedCode(t *testing.T) {
	d := newDevices(t.TempDir())
	code := newCode(t, d)
	typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	id, key, err := d.Pair(typed, mbp)
	if err != nil {
		t.Fatalf("pairing with %q: %v", typed, err)
	}
	if !signing.ValidKey(key) || id == "" {
		t.Fatalf("id %q, key %q", id, key)
	}
	if _, _, err := d.Pair(code, mbp); !errors.Is(err, ErrPairing) {
		t.Errorf("second use of a code: %v, want ErrPairing", err)
	}
	dev, ok := d.Lookup(id)
	if !ok || dev.Key != key || dev.DNSName != mbp.DNSName {
		t.Errorf("Lookup = %+v, %v", dev, ok)
	}
	for _, listed := range d.List() {
		if listed.Key != "" {
			t.Error("List exposes a key")
		}
	}
}

func TestCodeWithdrawnAfterTooManyGuesses(t *testing.T) {
	d := newDevices(t.TempDir())
	code := newCode(t, d)
	for i := range codeAttempts {
		if _, _, err := d.Pair("AAAA-AAAA", mbp); !errors.Is(err, ErrPairing) {
			t.Fatalf("wrong guess %d: %v", i, err)
		}
	}
	if _, _, err := d.Pair(code, mbp); !errors.Is(err, ErrPairing) {
		t.Errorf("the right code after %d wrong guesses: %v, want ErrPairing", codeAttempts, err)
	}
}

func TestExpiredCode(t *testing.T) {
	d := newDevices(t.TempDir())
	code := newCode(t, d)
	d.expires = time.Now().Add(-time.Second)
	if _, _, err := d.Pair(code, mbp); !errors.Is(err, ErrPairing) {
		t.Errorf("expired code: %v, want ErrPairing", err)
	}
}

func TestPairingsPerInstanceAndPersisted(t *testing.T) {
	dir := t.TempDir()
	d := newDevices(dir)
	pair := func(instance string) string {
		dev := mbp
		dev.Instance = instance
		id, _, err := d.Pair(newCode(t, d), dev)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := pair("aa")
	second := pair("bb")
	again := pair("aa") // replaces the first, keeps the second
	if _, ok := d.Lookup(first); ok {
		t.Error("pairing the same instance again kept the old pairing")
	}
	if _, ok := d.Lookup(second); !ok {
		t.Error("pairing one instance removed another instance's pairing")
	}
	st, err := os.Stat(filepath.Join(dir, "share-devices.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("devices file: %v, mode %v", err, st.Mode())
	}
	reloaded := newDevices(dir)
	for _, id := range []string{second, again} {
		if _, ok := reloaded.Lookup(id); !ok {
			t.Errorf("pairing %s lost on reload", id)
		}
	}
}

func TestRevoke(t *testing.T) {
	d := newDevices(t.TempDir())
	for _, inst := range []string{"aa", "bb"} {
		dev := mbp
		dev.Instance = inst
		if _, _, err := d.Pair(newCode(t, d), dev); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := d.Revoke("MBP"); ok || !errors.Is(err, ErrAmbiguous) {
		t.Errorf("revoke by a shared host name: %v, %v; want ErrAmbiguous", ok, err)
	}
	id := d.List()[0].ID
	if ok, err := d.Revoke(id); !ok || err != nil {
		t.Fatalf("revoke by ID: %v, %v", ok, err)
	}
	if ok, err := d.Revoke("MBP"); !ok || err != nil {
		t.Errorf("revoke by a now unique host name: %v, %v", ok, err)
	}
	if ok, err := d.Revoke("nobody"); ok || err != nil {
		t.Errorf("revoke of an unknown device: %v, %v", ok, err)
	}
	if n := len(d.List()); n != 0 {
		t.Errorf("%d devices left", n)
	}
}

func TestRecordWithoutValidKeyNeverMatches(t *testing.T) {
	dir := t.TempDir()
	bad := `[{"id":"abc","name":"MBP","dnsName":"mbp.t.ts.net"},{"id":"def","dnsName":"x","key":"zz"}]`
	if err := os.WriteFile(filepath.Join(dir, "share-devices.json"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	d := newDevices(dir)
	for _, id := range []string{"abc", "def"} {
		if _, ok := d.Lookup(id); ok {
			t.Errorf("Lookup(%s) accepted a record without a valid key", id)
		}
	}
}

func TestFailedSaveChangesNothing(t *testing.T) {
	dir := t.TempDir()
	d := newDevices(dir)
	id, _, err := d.Pair(newCode(t, d), mbp)
	if err != nil {
		t.Fatal(err)
	}
	// Make the rename fail: a directory where the file should go.
	path := filepath.Join(dir, "share-devices.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Revoke(id); err == nil {
		t.Fatal("revoke succeeded although the file could not be written")
	}
	if _, ok := d.Lookup(id); !ok {
		t.Error("a failed revoke removed the device anyway, so it cannot be retried")
	}
}
