package shell

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func TestLiveAssociationHashes(t *testing.T) {
	if os.Getenv("MYFILES_ASSOC_READ_TEST") == "" {
		t.Skip("read-only live association check is opt-in")
	}
	h, err := associationContext()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("UserChoiceLatest=%v", h.latest)
	for _, ext := range []string{"txt", "jpg", "png", "pdf"} {
		c, err := readChoice(ext, h.latest)
		if err != nil {
			t.Fatal(err)
		}
		if !c.Exists {
			continue
		}
		if got := h.hash(c); got != c.Hash {
			t.Errorf(".%s checksum mismatch: computed %s, stored %s", ext, got, c.Hash)
		}
	}
}

func TestTemporaryDefaultAssociation(t *testing.T) {
	if os.Getenv("MYFILES_ASSOC_WRITE_TEST") == "" {
		t.Skip("temporary extension registry test is opt-in")
	}
	h, err := associationContext()
	if err != nil {
		t.Fatal(err)
	}
	ext := fmt.Sprintf("myfiles-test-%d", time.Now().UnixNano())
	id := "MyFiles.Test." + ext
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	classPath := `Software\Classes\` + id
	extPath := `Software\Classes\.` + ext
	// All cleanup targets include the newly generated unique test identifier;
	// neither real image associations nor RegisteredApplications are touched.
	t.Cleanup(func() {
		if err := h.restore(choice{Extension: ext, Latest: h.latest}); err != nil {
			t.Error(err)
		}
		for _, p := range []string{extPath + `\OpenWithProgids`, extPath, classPath + `\shell\open\command`, classPath + `\shell\open`, classPath + `\shell`, classPath, fileExts + "." + ext + `\OpenWithList`, fileExts + "." + ext} {
			if err := registry.DeleteKey(registry.CURRENT_USER, p); err != nil && err != registry.ErrNotExist {
				t.Errorf("cleanup %s: %v", p, err)
			}
		}
		notifyAssociations()
	})
	k, _, err := registry.CreateKey(registry.CURRENT_USER, classPath+`\shell\open\command`, registry.WRITE)
	if err != nil {
		t.Fatal(err)
	}
	err = k.SetStringValue("", `"`+exe+`" "%1"`)
	k.Close()
	if err != nil {
		t.Fatal(err)
	}
	k, _, err = registry.CreateKey(registry.CURRENT_USER, extPath+`\OpenWithProgids`, registry.WRITE)
	if err != nil {
		t.Fatal(err)
	}
	err = k.SetStringValue(id, "")
	k.Close()
	if err != nil {
		t.Fatal(err)
	}
	notifyAssociations()
	empty := choice{Extension: ext, Latest: h.latest}
	rejected := errors.New("simulated shell rejection")
	if err := h.changeChoice(empty, id, func() error { return rejected }); !errors.Is(err, rejected) {
		t.Fatalf("new choice rejection: %v", err)
	}
	if c, err := readChoice(ext, h.latest); err != nil || c.Exists {
		t.Fatalf("rejected new choice was not removed: %+v, %v", c, err)
	}
	path := `C:\nowhere\sample.` + ext
	apps := Apps(path)
	var selected App
	for _, app := range apps {
		if strings.EqualFold(app.ID, exe) {
			selected = app
		}
	}
	if selected.ID == "" {
		t.Fatalf("temporary handler not listed: %+v", apps)
	}
	if err := SetDefaultApp(path, selected); err != nil {
		t.Fatal(err)
	}
	notifyAssociations()
	c, err := readChoice(ext, h.latest)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hash != h.hash(c) {
		t.Fatal("written hash does not match key timestamp")
	}
	got, err := EffectiveDefaultFor(ext)
	if err != nil || !strings.EqualFold(got, id) {
		t.Fatalf("shell got %q, %v; wanted %q", got, err, id)
	}
	t.Logf("Windows shell accepted %s for temporary extension .%s", got, ext)
	// Exercise restoration of an existing choice too, not just deletion.
	if err := h.restore(c); err != nil {
		t.Fatal(err)
	}
	notifyAssociations()
	if got, err := EffectiveDefaultFor(ext); err != nil || !strings.EqualFold(got, id) {
		t.Fatalf("restore: %q %v", got, err)
	}
	c, err = readChoice(ext, h.latest)
	if err != nil {
		t.Fatal(err)
	}
	// A stale backup must not overwrite a newer choice.
	if err := h.changeChoice(empty, id, func() error { t.Fatal("verified a stale choice"); return nil }); err == nil {
		t.Fatal("accepted a stale backup")
	}
	// Reject a replacement after the write and verify that Windows resolves
	// the original ProgID again, with a checksum for its new timestamp.
	if err := h.changeChoice(c, id+".Rejected", func() error { return rejected }); !errors.Is(err, rejected) {
		t.Fatalf("existing choice rejection: %v", err)
	}
	if err := verifyDefault(ext, id); err != nil {
		t.Fatalf("rollback did not restore effective default: %v", err)
	}
	c, err = readChoice(ext, h.latest)
	if err != nil || c.Hash != h.hash(c) {
		t.Fatalf("rollback checksum: %+v, %v", c, err)
	}
	if err := h.changeChoice(c, "invalid\x00id", func() error { t.Fatal("verified an invalid ID"); return nil }); err == nil {
		t.Fatal("accepted an invalid ProgID")
	}
	if after, err := readChoice(ext, h.latest); err != nil || after != c {
		t.Fatalf("failed write changed the original record: %+v, %v", after, err)
	}
}
