package parsetorrent

import (
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/sagan/ptool/cmd"
	"github.com/sagan/ptool/torrentutil"
	"github.com/sagan/ptool/utils"
)

var command = &cobra.Command{
	Use:     "parsetorrent file.torrent...",
	Aliases: []string{"parse"},
	Short:   "Parse torrent files and show their content.",
	Long:    `Parse torrent files and show their content.`,
	Args:    cobra.MatchAll(cobra.MinimumNArgs(1), cobra.OnlyValidArgs),
	Run:     parsetorrent,
}

var (
	showAll = false
)

func init() {
	command.Flags().BoolVarP(&showAll, "all", "a", false, "Show all info")
	cmd.RootCmd.AddCommand(command)
}

func parsetorrent(cmd *cobra.Command, args []string) {
	torrentFilenames := utils.ParseFilenameArgs(args...)
	errorCnt := int64(0)

	for i, torrentFileName := range torrentFilenames {
		if showAll && i > 0 {
			fmt.Printf("\n")
		}
		torrentInfo, err := torrentutil.ParseTorrentFile(torrentFileName, 99)
		if err != nil {
			log.Printf("Failed to parse %s: %v", torrentFileName, err)
			errorCnt++
			continue
		}
		torrentInfo.Print(torrentFileName, showAll)
		if showAll {
			torrentInfo.PrintFiles(true, false)
		}
	}
	if errorCnt > 0 {
		os.Exit(1)
	}
}
