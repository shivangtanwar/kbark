// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/shivangtanwar/kbark/internal/transcript"
)

var transcriptsCmd = &cobra.Command{
	Use:   "transcripts",
	Short: "List or open saved AI diagnosis transcripts",
	Long: `kbark saves a markdown transcript of every "?" diagnosis to
~/.cache/kbark/diagnoses/ (or the platform equivalent). Use
"kbark transcripts list" to see them. Disable saving by setting
KBARK_TRANSCRIPTS=off in your environment.`,
}

var transcriptsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved diagnosis transcripts (newest first)",
	RunE: func(_ *cobra.Command, _ []string) error {
		dir, err := transcript.DefaultDir()
		if err != nil {
			return fmt.Errorf("resolve cache dir: %w", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(os.Stdout, "no transcripts yet (would be saved to %s)\n", dir)
				return nil
			}
			return fmt.Errorf("read transcript dir: %w", err)
		}

		type item struct {
			name    string
			modTime time.Time
			size    int64
		}
		items := make([]item, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			items = append(items, item{name: e.Name(), modTime: info.ModTime(), size: info.Size()})
		}
		if len(items) == 0 {
			fmt.Fprintf(os.Stdout, "no transcripts in %s\n", dir)
			return nil
		}
		sort.Slice(items, func(i, j int) bool { return items[i].modTime.After(items[j].modTime) })
		fmt.Fprintf(os.Stdout, "%s\n\n", dir)
		for _, it := range items {
			fmt.Fprintf(os.Stdout, "%-19s  %5d KiB  %s\n",
				it.modTime.Local().Format("2006-01-02 15:04:05"),
				it.size/1024,
				it.name)
		}
		return nil
	},
}

func init() {
	transcriptsCmd.AddCommand(transcriptsListCmd)
	rootCmd.AddCommand(transcriptsCmd)
}
