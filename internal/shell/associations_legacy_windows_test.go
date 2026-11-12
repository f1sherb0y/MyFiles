package shell

import (
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Exercise the Windows 10 registry layout even when the host uses Latest.
// Only a unique test extension is written; no image defaults are changed.
func TestLegacyChoiceRoundTrip(t *testing.T) {
	if os.Getenv("MYFILES_ASSOC_WRITE_TEST") == "" {
		t.Skip("temporary extension registry test is opt-in")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	h := hashContext{sid: user.User.Sid.String()}
	ext := fmt.Sprintf("association-legacy-test-%d", time.Now().UnixNano())
	empty := choice{Extension: ext}
	t.Cleanup(func() {
		if err := h.restore(empty); err != nil {
			t.Error(err)
		}
		if err := registry.DeleteKey(registry.CURRENT_USER, fileExts+"."+ext); err != nil {
			t.Error(err)
		}
	})
	if err := h.writeChoice(ext, "Test.Original", false); err != nil {
		t.Fatal(err)
	}
	original, err := readChoice(ext, false)
	if err != nil || !original.Exists || original.Hash != h.hash(original) {
		t.Fatalf("legacy write: %+v, %v", original, err)
	}
	if err := h.writeChoice(ext, "Test.Replacement", false); err != nil {
		t.Fatal(err)
	}
	if err := h.restore(original); err != nil {
		t.Fatal(err)
	}
	got, err := readChoice(ext, false)
	if err != nil || got.ProgID != original.ProgID || got.Hash != h.hash(got) {
		t.Fatalf("legacy rollback: %+v, %v", got, err)
	}
	if err := h.restore(empty); err != nil {
		t.Fatal(err)
	}
	if got, err := readChoice(ext, false); err != nil || got.Exists {
		t.Fatalf("legacy cleanup: %+v, %v", got, err)
	}
}
