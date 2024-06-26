package importsites

import (
	"fmt"
	"slices"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/sagan/ptool/cmd/cookiecloud"
	"github.com/sagan/ptool/config"
	"github.com/sagan/ptool/site"
	"github.com/sagan/ptool/site/tpl"
	"github.com/sagan/ptool/util"
	"github.com/sagan/ptool/util/helper"
)

var (
	force           = false
	noCheck         = false
	profile         = ""
	siteProxy       = ""
	siteImpersonate = ""
	siteUa          = ""
	siteJa3         = ""
)

var command = &cobra.Command{
	Use:         "import",
	Annotations: map[string]string{"cobra-prompt-dynamic-suggestions": "cookiecloud.import"},
	Short:       "Import sites from cookies of cookiecloud servers.",
	Long: `Import sites from cookies of cookiecloud servers.
It will get latest cookies from cookiecloud servers, find sites that do NOT exist in config file currently,
Test their cookies are valid, then add them to config file.

It will ask for confirm before updating config file, unless --force flag is set.
Be aware that all existing comments in config file will be LOST when updating config file.`,
	RunE: importsites,
}

func init() {
	command.Flags().BoolVarP(&force, "force", "", false,
		"Do update the config file without confirm. Be aware that all existing comments in config file will be LOST")
	command.Flags().BoolVarP(&noCheck, "skip-check", "", false, "Skip site cookie validity checking prior to importing")
	command.Flags().StringVarP(&profile, "profile", "", "",
		"Comma-separated, Set the used cookiecloud profile name(s). "+
			"If not set, All cookiecloud profiles in config will be used")
	command.Flags().StringVarP(&siteProxy, "site-proxy", "", "", "Set the proxy for imported sites")
	command.Flags().StringVarP(&siteImpersonate, "site-impersonate", "", "", "Set the impersonate for imported sites")
	command.Flags().StringVarP(&siteUa, "site-ua", "", "", "Set the user-agent for imported sites")
	command.Flags().StringVarP(&siteJa3, "site-ja3", "", "", "Set the client TLS ja3 fingerprint for imported sites")
	cookiecloud.Command.AddCommand(command)
}

func importsites(cmd *cobra.Command, args []string) error {
	errorCnt := int64(0)
	cookiecloudProfiles := cookiecloud.ParseProfile(profile)
	if len(cookiecloudProfiles) == 0 {
		return fmt.Errorf("no cookiecloud profile specified or found")
	}
	cookiecloudDatas := []cookiecloud.Ccdata_struct{}
	for _, profile := range cookiecloudProfiles {
		data, err := cookiecloud.GetCookiecloudData(profile.Server, profile.Uuid, profile.Password,
			config.GetProxy(profile.Proxy), util.FirstNonZeroIntegerArg(config.Timeout, profile.Timeout))
		if err != nil {
			log.Errorf("Cookiecloud server %s (uuid %s) connection failed: %v\n", profile.Server, profile.Uuid, err)
			errorCnt++
		} else {
			log.Infof("Cookiecloud server %s (uuid %s) connection ok: cookies of %d domains found\n",
				profile.Server, profile.Uuid, len(data.Cookie_data))
			cookiecloudDatas = append(cookiecloudDatas, cookiecloud.Ccdata_struct{
				Label: fmt.Sprintf("%s-%s", util.GetUrlDomain(profile.Server), profile.Uuid),
				Data:  data,
			})
		}
	}
	if len(cookiecloudDatas) == 0 {
		return fmt.Errorf("no cookiecloud server can be connected")
	}

	addSites := []*config.SiteConfigStruct{}
	tplExistingFlags := map[string]bool{}
	for _, tplname := range tpl.SITENAMES {
		tplInfo := tpl.SITES[tplname]
		for _, site := range config.Get().Sites {
			if site.Type == tplname || slices.Contains(tplInfo.Aliases, site.Type) {
				tplExistingFlags[tplname] = true
				break
			}
		}
		if sitename, _ := tpl.GuessSiteByDomain(util.ParseUrlHostname(tplInfo.Url), ""); sitename != "" {
			tplExistingFlags[tplname] = true
		}
	}
	nowStr := util.FormatTime(util.Now())
	for _, cookiecloudData := range cookiecloudDatas {
		for _, tplname := range tpl.SITENAMES {
			if tplExistingFlags[tplname] {
				continue
			}
			if tpl.SITES[tplname].Dead || tpl.SITES[tplname].NoCookie {
				log.Debugf("Internal site %s is dead or does not use cookie authorization, skip it", tplname)
				continue
			}
			cookie, rawCookies, _ := cookiecloudData.Data.GetEffectiveCookie(tpl.SITES[tplname].Url, false, "http")
			if cookie == "" || !slices.ContainsFunc(rawCookies, func(rc *cookiecloud.Cookie) bool {
				return !rc.IsCDN()
			}) {
				continue
			}
			newsiteconfig := &config.SiteConfigStruct{Type: tplname, Cookie: cookie, Proxy: siteProxy,
				Impersonate: siteImpersonate, UserAgent: siteUa, Ja3: siteJa3}
			if !noCheck {
				siteInstance, err := site.CreateSiteInternal(tplname, newsiteconfig, config.Get())
				if err != nil {
					log.Debugf("New Site %s from cookiecloud %s is invalid (create instance error: %v",
						tplname, cookiecloudData.Label, err)
					continue
				}
				sitestatus, err := siteInstance.GetStatus()
				if err != nil {
					log.Debugf("New Site %s from cookiecloud %s is invalid (status error=%v)",
						tplname, cookiecloudData.Label, err)
					continue
				}
				if !sitestatus.IsOk() {
					log.Debugf("New Site %s from cookiecloud %s is invalid (invalid status)",
						tplname, cookiecloudData.Label)
					continue
				}
				log.Infof("✓✓New site %s from cookiecloud %s is valid (username: %s)",
					tplname, cookiecloudData.Label, sitestatus.UserName)
			}
			sitename := ""
			if config.GetSiteConfig(tplname) != nil {
				i := 1
				for {
					sitename = fmt.Sprint(tplname, i)
					if config.GetSiteConfig(sitename) == nil {
						break
					}
					i++
				}
			}
			log.Infof("Add new site type=%s, name=%s", tplname, sitename)
			addSites = append(addSites, &config.SiteConfigStruct{
				Name:   sitename,
				Type:   tplname,
				Cookie: cookie,
				AutoComment: fmt.Sprintf(`imported by "ptool cookiecloud import" at %s from cookiecloud %s`,
					nowStr, cookiecloudData.Label),
			})
			tplExistingFlags[tplname] = true
		}
	}

	if len(addSites) > 0 {
		fmt.Printf("✓new sites found (%d): %s\n", len(addSites),
			strings.Join(util.Map(addSites, func(site *config.SiteConfigStruct) string {
				sitename := site.Type
				if site.Name != "" {
					sitename = fmt.Sprintf("%s (as %s)", sitename, site.Name)
				}
				return sitename
			}), ", "))
		configFile := fmt.Sprintf("%s/%s", config.ConfigDir, config.ConfigFile)
		if !force && !helper.AskYesNoConfirm(
			fmt.Sprintf("Will update the config file (%s). Be aware that all existing comments will be LOST",
				configFile)) {
			return fmt.Errorf("abort")
		}
		config.UpdateSites(addSites)
		err := config.Set()
		if err == nil {
			fmt.Printf("Successfully update config file %s\n", configFile)
			return nil
		} else {
			return fmt.Errorf("failed to update config file %s : %w", configFile, err)
		}
	} else {
		fmt.Printf("!No new sites found in cookiecloud datas\n")
	}

	if errorCnt > 0 {
		return fmt.Errorf("%d errors", errorCnt)
	}
	return nil
}
