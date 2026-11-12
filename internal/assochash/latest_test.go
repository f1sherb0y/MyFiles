package assochash

import (
	"strings"
	"testing"
)

func TestLatestReferenceVector(t *testing.T) {
	input := "copyright (c) microsoft. all rights reserved {3822b7ca-c2f4-4889-b8cc-4ce39a8fb81c}.pdd01dcb06ea49f32a0b4deb148-0249-44c4-a8d3-5409e822c599msedgepdfs-1-5-21-673349297-2269585490-1023937497-500"
	if got := latestCanonical(input); got != "JOBZ2dl4dKM=" {
		t.Fatalf("got %s", got)
	}
}

func TestLatestTimestampAndClasses(t *testing.T) {
	// The class is selected using the original MachineID's final character.
	for _, machine := range []string{"{00000000-0000-0000-0000-000000000000}", "{00000000-0000-0000-0000-000000000001}", "{00000000-0000-0000-0000-000000000002}"} {
		got := Latest(".jpg", "myimages.jpg", "S-1-5-21-1001", machine, 132000000000000001)
		if got == "" || got != Latest(".JPG", "MYIMAGES.JPG", "s-1-5-21-1001", machine, 132000000000009999) {
			t.Fatal("case normalization or millisecond truncation")
		}
		if got == Latest(".png", "myimages.jpg", "S-1-5-21-1001", machine, 132000000000000001) {
			t.Fatal("extension did not affect hash")
		}
	}
}

func TestLatestRepetitiveInput(t *testing.T) {
	// Independent upstream CLI output, including repeated-token encoding.
	if got := latestCanonical(strings.Repeat("x", 400)); got != "rNrHcmqriPc=" {
		t.Fatalf("repeat vector: %q", got)
	}
	// This input stalled the upstream codec. Unsupported inputs must return
	// failure rather than keep the default-setting worker busy indefinitely.
	for _, input := range []string{strings.Repeat("abc123", 100), strings.Repeat("x", 4097)} {
		if got := latestCanonical(input); got != "" {
			t.Fatalf("expected unsupported input, got %q", got)
		}
	}
}

func TestLegacyReferenceVector(t *testing.T) {
	// Independently calculated with PS-SFTA's Get-Hash implementation.
	const stamp = uint64(132000000000000000)
	for _, offset := range []uint64{0, 1, 599999999} {
		if got := Legacy(".JPG", "MYIMAGES.JPG", "S-1-5-21-1001", stamp+offset); got != "GkByFpwyOlI=" {
			t.Fatalf("offset %d: %q", offset, got)
		}
	}
}
