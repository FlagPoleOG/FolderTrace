package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Mark is the person's verdict on a label. Marks live in FolderTrace's own folder.
type Mark struct {
	Right  bool   `json:"right"`
	Actual string `json:"actual,omitempty"`
}

type Marks map[string]Mark

func marksPath(dir string) string { return filepath.Join(dir, "marks.json") }

func LoadMarks(dir string) Marks {
	m := Marks{}
	data, err := os.ReadFile(marksPath(dir))
	if err != nil {
		return m
	}
	if json.Unmarshal(data, &m) != nil {
		return Marks{}
	}
	return m
}

// SaveMarks writes only to FolderTrace's own marks file.
func SaveMarks(dir string, m Marks) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(marksPath(dir), data, 0o644)
}

func markKey(f *Folder) string { return strings.ToLower(f.Rel) }

// MarkScore counts marks for folders in this scan only.
func MarkScore(all []*Folder, m Marks) (right, total int) {
	for _, f := range all {
		if v, ok := m[markKey(f)]; ok {
			total++
			if v.Right {
				right++
			}
		}
	}
	return right, total
}

func csvSafe(s string) string {
	if s != "" && strings.ContainsAny(s[:1], "=+-@\t\r") {
		return "'" + s // stops a spreadsheet treating a folder name as a formula
	}
	return s
}

// ReportCSV builds the report. It holds folder names and labels, not file contents.
func ReportCSV(all []*Folder, m Marks, now time.Time) []byte {
	var b bytes.Buffer
	b.WriteString("\xEF\xBB\xBF")
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"Folder", "Location", "Belongs to", "Named from evidence", "Status", "Confidence", "Size (bytes)", "Size", "Files",
		"Last changed", "Last changed date", "Contains", "May hold saves", "Why", "Advice", "Evidence", "Related folders", "Your check", "What it really is"})
	for _, f := range all {
		check, actual := "", ""
		if v, ok := m[markKey(f)]; ok {
			check = "Wrong"
			if v.Right {
				check = "Right"
			}
			actual = v.Actual
		}
		date := ""
		if !f.Newest.IsZero() {
			date = f.Newest.Format("2006-01-02")
		}
		yes := func(on bool) string {
			if on {
				return "Yes"
			}
			return "No"
		}
		_ = w.Write([]string{csvSafe(f.Name), csvSafe(f.Rel), csvSafe(f.Owner), yes(f.Identified), f.Verdict.String(), f.Confidence,
			fmt.Sprint(f.Size), FormatSize(f.Size), fmt.Sprint(f.Files), FormatAge(f.Newest, now), date, f.Contains(), yes(f.Saves),
			csvSafe(f.Why), csvSafe(f.Advice), csvSafe(strings.Join(f.Evidence, "; ")), csvSafe(RelatedText(f, now)), check, csvSafe(actual)})
	}
	w.Flush()
	return b.Bytes()
}

// dataDirOverride points the demo build at its own folder.
var dataDirOverride string

var copyOldOnce sync.Once

// copyOldDataDir copies marks from the folder versions before the rename used, so
// people keep their right and wrong marks. It only reads the old folder and leaves it
// in place.
func copyOldDataDir(old, dir string) {
	copyOldOnce.Do(func() {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			return
		}
		entries, err := os.ReadDir(old)
		if err != nil || os.MkdirAll(dir, 0o755) != nil {
			return
		}
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(old, e.Name())); err == nil {
				_ = os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644)
			}
		}
	})
}
