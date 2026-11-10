package loki

import (
	"strings"
	"testing"
)

func TestEveryLogQLConstructClassified(t *testing.T) {
	inventory := Inventory()
	if len(inventory) == 0 {
		t.Fatal("inventory is empty")
	}
	seen := make(map[string]bool, len(inventory))
	for _, construct := range inventory {
		t.Run(construct.Name, func(t *testing.T) {
			if construct.Name == "" || construct.Query == "" {
				t.Fatal("construct has an empty name or query")
			}
			if seen[construct.Name] {
				t.Fatalf("duplicate construct %q", construct.Name)
			}
			seen[construct.Name] = true
			if construct.Class != Supported && construct.Class != Rejected {
				t.Fatalf("class = %q, want supported or error", construct.Class)
			}
			_, err := Parse(construct.Query)
			switch construct.Class {
			case Supported:
				if err != nil {
					t.Fatalf("Parse(%q) = %v, want support", construct.Query, err)
				}
			case Rejected:
				if err == nil {
					t.Fatalf("Parse(%q) succeeded, want an explicit error", construct.Query)
				}
				if !strings.HasPrefix(err.Error(), "loki:") && !strings.Contains(err.Error(), "loki:") {
					t.Fatalf("error = %q, want a loki error", err)
				}
			}
		})
	}
}

func TestClassifyMatchesParse(t *testing.T) {
	for _, construct := range Inventory() {
		if got := Classify(construct.Query); got != construct.Class {
			t.Errorf("Classify(%q) = %q, want %q", construct.Query, got, construct.Class)
		}
	}
}

func TestInventoryCoversEveryRejectedStage(t *testing.T) {
	present := make(map[string]bool)
	for _, construct := range Inventory() {
		if construct.Class == Rejected {
			present[construct.Name] = true
		}
	}
	for _, name := range []string{
		"pattern parser", "regexp parser", "unpack parser", "ip parser", "pack parser",
		"line_format stage", "label_format stage", "drop stage", "keep stage",
		"decolorize stage", "unwrap stage", "label_filter stage",
	} {
		if !present[name] {
			t.Errorf("inventory is missing the rejected construct %q", name)
		}
	}
}
