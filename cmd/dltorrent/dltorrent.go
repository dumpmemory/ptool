package dltorrent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sagan/ptool/cmd"
	"github.com/sagan/ptool/util"
	"github.com/sagan/ptool/util/helper"
	"github.com/sagan/ptool/util/torrentutil"
)

var command = &cobra.Command{
	Use:         "dltorrent {torrentId | torrentUrl}... [--dir dir]",
	Annotations: map[string]string{"cobra-prompt-dynamic-suggestions": "dltorrent"},
	Short:       "Download site torrents to local.",
	Long: `Download site torrents to local.
Args is torrent list that each one could be a site torrent id (e.g.: "mteam.488424")
or url (e.g.: "https://kp.m-team.cc/details.php?id=488424").
Torrent url that does NOT belong to any site (e.g.: a public site url) is also supported.

To set the filename of downloaded torrent, use --rename <name> flag,
which supports the following variable placeholders:
* [size] : Torrent size
* [id] :  Torrent id in site
* [site] : Torrent site
* [filename] : Original torrent filename without ".torrent" extension
* [filename128] : The prefix of [filename] which is at max 128 bytes
* [name] : Torrent name
* [name128] : The prefix of torrent name which is at max 128 bytes`,
	Args: cobra.MatchAll(cobra.MinimumNArgs(1), cobra.OnlyValidArgs),
	RunE: dltorrent,
}

var (
	downloadSkipExisting = false
	slowMode             = false
	downloadDir          = ""
	rename               = ""
	defaultSite          = ""
	errSkipExisting      = errors.New("skip existing torrent")
)

func init() {
	command.Flags().BoolVarP(&downloadSkipExisting, "download-skip-existing", "", false,
		`Do NOT re-download torrent that same name file already exists in local dir. `+
			`If this flag is set, the download torrent filename ("--rename" flag) will be fixed to `+
			`"[site].[id].torrent" (e.g.: "mteam.12345.torrent") format`)
	command.Flags().BoolVarP(&slowMode, "slow", "", false, "Slow mode. wait after downloading each torrent")
	command.Flags().StringVarP(&defaultSite, "site", "", "", "Set default site of torrents")
	command.Flags().StringVarP(&downloadDir, "download-dir", "", ".", `Set the dir of downloaded torrents. `+
		`Use "-" to directly output torrent content to stdout`)
	command.Flags().StringVarP(&rename, "rename", "", "", "Rename downloaded torrents (supports variables)")
	cmd.RootCmd.AddCommand(command)
}

// @todo: currently, --download-skip-existing flag will NOT work if (torrent) arg is a site torrent url,
// to fix it the site.Site interface must be changed to separate torrent url parsing from downloading.
func dltorrent(cmd *cobra.Command, args []string) error {
	errorCnt := int64(0)
	torrents := args
	outputToStdout := false
	if downloadDir == "-" {
		if len(torrents) > 1 {
			return fmt.Errorf(`"--download-dir -" can only be used to download one torrent`)
		} else {
			outputToStdout = true
		}
	}
	var beforeDownload func(sitename string, id string) error
	if downloadSkipExisting {
		if rename != "" {
			return fmt.Errorf("--download-skip-existing and --rename flags are NOT compatible")
		}
		if outputToStdout {
			return fmt.Errorf(`--download-skip-existing can NOT be used with "--download-dir -"`)
		}
		beforeDownload = func(sitename, id string) error {
			if sitename != "" && id != "" {
				filename := fmt.Sprintf("%s.%s.torrent", sitename, id)
				if util.FileExists(filepath.Join(downloadDir, filename)) {
					log.Debugf("Skip downloading local-existing torrent %s.%s", sitename, id)
					return errSkipExisting
				}
			}
			return nil
		}
	}
	for i, torrent := range torrents {
		if i > 0 && slowMode {
			util.Sleep(3)
		}
		content, tinfo, _, sitename, _filename, id, _, err :=
			helper.GetTorrentContent(torrent, defaultSite, false, true, nil, true, beforeDownload)
		if outputToStdout {
			if err != nil {
				errorCnt++
				fmt.Fprintf(os.Stderr, "Failed to download torrent: %v\n", err)
			} else if term.IsTerminal(int(os.Stdout.Fd())) {
				errorCnt++
				fmt.Fprintf(os.Stderr, "Torrent binary file will mess up the terminal. Use pipe to redirect stdout\n")
			} else if _, err = os.Stdout.Write(content); err != nil {
				errorCnt++
				fmt.Fprintf(os.Stderr, "Failed to output torrent content to stdout: %v\n", err)
			}
			continue
		}
		if err != nil {
			if err == errSkipExisting {
				fmt.Printf("- %s (site=%s): skip due to exists in local dir (%s.%s.torrent)\n",
					torrent, sitename, sitename, id)
			} else {
				fmt.Printf("✕ %s (site=%s): %v\n", torrent, sitename, err)
				errorCnt++
			}
			continue
		}
		filename := ""
		if downloadSkipExisting && sitename != "" && id != "" {
			filename = fmt.Sprintf("%s.%s.torrent", sitename, id)
		} else if rename == "" {
			filename = _filename
		} else {
			filename = torrentutil.RenameTorrent(rename, sitename, id, _filename, tinfo)
		}
		err = os.WriteFile(filepath.Join(downloadDir, filename), content, 0666)
		if err != nil {
			fmt.Printf("✕ %s (site=%s): failed to save to %s/: %v\n", filename, sitename, downloadDir, err)
			errorCnt++
		} else {
			fmt.Printf("✓ %s (site=%s): saved to %s/\n", filename, sitename, downloadDir)
		}
	}
	if errorCnt > 0 {
		return fmt.Errorf("%d errors", errorCnt)
	}
	return nil
}
