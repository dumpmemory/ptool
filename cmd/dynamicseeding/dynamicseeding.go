package dynamicseeding

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/sagan/ptool/client"
	"github.com/sagan/ptool/cmd"
	"github.com/sagan/ptool/config"
	"github.com/sagan/ptool/constants"
	"github.com/sagan/ptool/site"
	"github.com/sagan/ptool/util"
	"github.com/sagan/ptool/util/torrentutil"
)

const IGNORE_FILE_SIZE = 100

var command = &cobra.Command{
	Use:         "dynamicseeding {client} {site}",
	Annotations: map[string]string{"cobra-prompt-dynamic-suggestions": "dynamicseeding"},
	Short:       "Dynamic seeding torrents of sites.",
	Long:        `Dynamic seeding torrents of sites.`,
	Args:        cobra.MatchAll(cobra.ExactArgs(2), cobra.OnlyValidArgs),
	RunE:        dynamicseeding,
}

var (
	dryRun = false
)

func init() {
	command.Flags().BoolVarP(&dryRun, "dry-run", "d", false,
		"Dry run. Do NOT actually add or delete torrent to / from client")
	cmd.RootCmd.AddCommand(command)
}

func dynamicseeding(cmd *cobra.Command, args []string) error {
	clientName := args[0]
	sitename := args[1]
	lock := flock.New(filepath.Join(config.ConfigDir, fmt.Sprintf("dynamic-seeding-%s.lock", sitename)))
	if ok, err := lock.TryLock(); err != nil || !ok {
		return fmt.Errorf("unable to acquire lock: %v", err)
	}
	defer lock.Unlock()
	ignoreFile, err := os.OpenFile(filepath.Join(config.ConfigDir,
		fmt.Sprintf("dynamic-seeding-%s.ignore.txt", sitename)),
		os.O_CREATE|os.O_RDWR, constants.PERM)
	if err != nil {
		return fmt.Errorf("failed to open ignore file: %v", err)
	}
	defer ignoreFile.Close()
	var ignores []string
	if contents, err := io.ReadAll(ignoreFile); err != nil {
		return fmt.Errorf("failed to read ignore file: %v", err)
	} else {
		ignores = strings.Split(string(contents), "\n")
	}

	clientInstance, err := client.CreateClient(clientName)
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}
	siteInstance, err := site.CreateSite(sitename)
	if err != nil {
		return fmt.Errorf("failed to create site: %v", err)
	}
	result, err := doDynamicSeeding(clientInstance, siteInstance, ignores)
	if err != nil {
		return err
	}
	result.Print(os.Stdout)
	if dryRun {
		log.Warnf("Dry-run. Exit")
		return nil
	}

	var newIgnores []string
	errorCnt := int64(0)
	deletedSize := int64(0)
	addedSize := int64(0)
	tags := result.AddTorrentsOption.Tags
	for len(result.AddTorrents) > 0 || len(result.DeleteTorrents) > 0 {
		if len(result.AddTorrents) > 0 && (addedSize <= deletedSize || len(result.DeleteTorrents) == 0) {
			torrent := result.AddTorrents[0].Id
			if torrent == "" {
				torrent = result.AddTorrents[0].DownloadUrl
			}
			if contents, _, _, err := siteInstance.DownloadTorrent(torrent); err != nil {
				log.Errorf("Failed to download site torrent %s", torrent)
				errorCnt++
			} else if tinfo, err := torrentutil.ParseTorrent(contents); err != nil {
				log.Errorf("Failed to download site torrent %s: is not a valid torrent: %v", torrent, err)
				errorCnt++
			} else {
				var _tags []string
				_tags = append(_tags, tags...)
				if tinfo.IsPrivate() {
					_tags = append(_tags, config.PRIVATE_TAG)
				} else {
					_tags = append(_tags, config.PUBLIC_TAG)
				}
				result.AddTorrentsOption.Name = result.AddTorrents[0].Name
				result.AddTorrentsOption.Tags = _tags
				meta := map[string]int64{}
				if result.AddTorrents[0].Id != "" {
					if id := util.ParseInt(result.AddTorrents[0].ID()); id != 0 {
						meta["id"] = id
					}
				}
				if err := clientInstance.AddTorrent(contents, result.AddTorrentsOption, meta); err != nil {
					log.Errorf("Failed to add site torrent %s to client: %v", torrent, err)
					errorCnt++
				} else {
					addedSize += result.AddTorrents[0].Size
				}
			}
			result.AddTorrents = result.AddTorrents[1:]
		} else {
			if err := clientInstance.DeleteTorrents([]string{result.DeleteTorrents[0].InfoHash}, true); err != nil {
				log.Errorf("Failed to delete client torrent %s (%s): %v",
					result.DeleteTorrents[0].Name, result.DeleteTorrents[0].InfoHash, err)
				errorCnt++
			} else {
				deletedSize += result.DeleteTorrents[0].Size
				if result.DeleteTorrents[0].Meta["id"] > 0 {
					newIgnores = append(newIgnores, fmt.Sprint(result.DeleteTorrents[0].Meta["id"]))
				}
			}
			result.DeleteTorrents = result.DeleteTorrents[1:]
		}
	}
	if len(newIgnores) > 0 {
		ignores = append(ignores, newIgnores...)
		if len(ignores) > IGNORE_FILE_SIZE {
			ignores = ignores[len(ignores)-IGNORE_FILE_SIZE:]
		}
		ignoreFile.Truncate(0)
		ignoreFile.Seek(0, 0)
		ignoreFile.WriteString(strings.Join(ignores, "\n"))
	}
	if errorCnt > 0 {
		return fmt.Errorf("%d errors", errorCnt)
	}
	return nil
}