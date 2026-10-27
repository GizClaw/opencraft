package summary

import (
	"testing"
)

func TestSourceIDIsTheTranscriptCoordinate(t *testing.T) {
	if got := SourceID(0); got != "0" {
		t.Fatalf("SourceID(0) = %q, want \"0\"", got)
	}
	if got := SourceID(41); got != "41" {
		t.Fatalf("SourceID(41) = %q, want \"41\"", got)
	}

}
