package tests

import (
	"path/filepath"
	"testing"

	"github.com/sagnik26/macdoc/internal/inventory"
)

const home = "/Users/test"

func fixture() []inventory.Software {
	return []inventory.Software{
		{ID: "app:com.figma.Desktop", Name: "Figma", Kind: "app", BundleID: "com.figma.Desktop"},
		{ID: "app-name:Visual Studio Code", Name: "Visual Studio Code", Kind: "app"},
		{ID: "npm:typescript", Name: "typescript", Kind: "npm"},
	}
}

func TestBundleIDMatchIsHighConfidence(t *testing.T) {
	folders := []inventory.Folder{{Path: filepath.Join(home, "Library/Caches/com.figma.Desktop"), Bytes: 500}}
	owners, unattr := inventory.Attribute(home, fixture(), folders)
	if len(unattr) != 0 || len(owners) != 1 {
		t.Fatalf("owners=%+v unattributed=%+v", owners, unattr)
	}
	o := owners[0]
	if o.SoftwareID != "app:com.figma.Desktop" || o.Confidence != 0.9 || o.Bytes != 500 || o.Evidence == "" {
		t.Errorf("owner = %+v", o)
	}
}

func TestNameMatchIgnoresPunctuationAndCase(t *testing.T) {
	folders := []inventory.Folder{{Path: filepath.Join(home, "Library/Application Support/visual-studio-code"), Bytes: 9}}
	owners, _ := inventory.Attribute(home, fixture(), folders)
	if len(owners) != 1 || owners[0].SoftwareID != "app-name:Visual Studio Code" || owners[0].Confidence != 0.6 {
		t.Errorf("owners = %+v", owners)
	}
}

func TestLookAlikeNameIsNotMatched(t *testing.T) {
	// "FigmaHelper" contains "figma" but is a different program.
	folders := []inventory.Folder{{Path: filepath.Join(home, "Library/Caches/FigmaHelper"), Bytes: 1}}
	owners, unattr := inventory.Attribute(home, fixture(), folders)
	if len(owners) != 0 || len(unattr) != 1 {
		t.Errorf("owners=%+v unattributed=%+v", owners, unattr)
	}
}

func TestDotFolderInHomeMatchesPackage(t *testing.T) {
	folders := []inventory.Folder{{Path: filepath.Join(home, ".typescript"), Bytes: 3}}
	owners, _ := inventory.Attribute(home, fixture(), folders)
	if len(owners) != 1 || owners[0].SoftwareID != "npm:typescript" {
		t.Errorf("owners = %+v", owners)
	}
}

func TestGroupContainerContainingBundleIDMatches(t *testing.T) {
	folders := []inventory.Folder{{Path: filepath.Join(home, "Library/Group Containers/ABCD.com.figma.Desktop"), Bytes: 7}}
	owners, _ := inventory.Attribute(home, fixture(), folders)
	if len(owners) != 1 || owners[0].Confidence != 0.8 {
		t.Errorf("owners = %+v", owners)
	}
}

func TestShortNamesNeverMatch(t *testing.T) {
	sw := []inventory.Software{{ID: "npm:go", Name: "go", Kind: "npm"}}
	folders := []inventory.Folder{{Path: filepath.Join(home, ".go"), Bytes: 1}}
	owners, unattr := inventory.Attribute(home, sw, folders)
	if len(owners) != 0 || len(unattr) != 1 {
		t.Errorf("a 2-letter name should not attribute: owners=%+v unattributed=%+v", owners, unattr)
	}
}

func TestFoldersOutsideCandidatePlacesAreIgnored(t *testing.T) {
	folders := []inventory.Folder{
		{Path: filepath.Join(home, "Documents/Figma"), Bytes: 99},          // user files, not program data
		{Path: filepath.Join(home, "Library/Caches/Figma/deep"), Bytes: 4}, // nested below a candidate
	}
	owners, unattr := inventory.Attribute(home, fixture(), folders)
	if len(owners) != 0 || len(unattr) != 0 {
		t.Errorf("owners=%+v unattributed=%+v; both should be ignored", owners, unattr)
	}
}

func TestUnmatchedCandidateIsUnattributed(t *testing.T) {
	folders := []inventory.Folder{{Path: filepath.Join(home, "Library/Caches/com.unknown.tool"), Bytes: 42}}
	owners, unattr := inventory.Attribute(home, fixture(), folders)
	if len(owners) != 0 || len(unattr) != 1 || unattr[0].Bytes != 42 {
		t.Errorf("owners=%+v unattributed=%+v", owners, unattr)
	}
}

func TestOutputDoesNotDependOnInputOrder(t *testing.T) {
	a := inventory.Folder{Path: filepath.Join(home, "Library/Caches/com.figma.Desktop"), Bytes: 1}
	b := inventory.Folder{Path: filepath.Join(home, "Library/Application Support/Figma"), Bytes: 2}
	o1, _ := inventory.Attribute(home, fixture(), []inventory.Folder{a, b})
	o2, _ := inventory.Attribute(home, fixture(), []inventory.Folder{b, a})
	if len(o1) != len(o2) {
		t.Fatalf("lengths differ: %d vs %d", len(o1), len(o2))
	}
	for i := range o1 {
		if o1[i] != o2[i] {
			t.Errorf("order changed the result: %+v vs %+v", o1[i], o2[i])
		}
	}
}
