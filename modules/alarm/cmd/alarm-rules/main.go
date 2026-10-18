// Publishes a Site's alarm rules file (deploy/sites/<site>.alarm-rules.json) as the policies its
// Site assignment evaluates (#609). A rule whose content is unchanged is left alone, so running it
// again changes nothing; a changed rule becomes the next revision of its family.
//
//	ALARM_DATABASE_URL_FILE=<file holding the Alarm database URL> alarm-rules <rules file>
//	alarm-rules -print <rules file>   # the policies, without touching the database
//
// The Alarm database is reachable only from the application network; run it with the Alarm
// owner's identity, e.g. `docker run --rm --network container:<energy-api> --volumes-from
// <energy-api> -e ALARM_DATABASE_URL_FILE=… -v <dir>:/alarm-rules:ro --entrypoint
// /alarm-rules/alarm-rules <energy-api image> /alarm-rules/<site>.alarm-rules.json`.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmrules"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
)

func main() {
	printOnly := flag.Bool("print", false, "print the policies instead of publishing them")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: alarm-rules [-print] <rules file>")
		os.Exit(2)
	}
	if err := run(flag.Arg(0), *printOnly); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(rulesPath string, printOnly bool) error {
	compiled, err := alarmrules.Load(rulesPath)
	if err != nil {
		return err
	}
	if printOnly {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(compiled)
	}
	urlFile := strings.TrimSpace(os.Getenv("ALARM_DATABASE_URL_FILE"))
	if urlFile == "" {
		return fmt.Errorf("ALARM_DATABASE_URL_FILE is required")
	}
	databaseURL, err := os.ReadFile(urlFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(identitycontext.WithTenantID(context.Background(), compiled.TenantID), time.Minute)
	defer cancel()
	store, err := alarmservice.OpenPostgresStore(ctx, strings.TrimSpace(string(databaseURL)))
	if err != nil {
		return err
	}
	defer store.Close()

	publishedBy := "alarm-rules:" + filepath.Base(rulesPath)
	now := time.Now().UTC()
	for _, sitePolicy := range compiled.Policies {
		published, changed, err := store.PublishSitePolicy(ctx, compiled.TenantID, compiled.SiteID, sitePolicy.Policy, sitePolicy.AssignmentID, now, publishedBy)
		if err != nil {
			return fmt.Errorf("%s: %w", sitePolicy.Policy.Title, err)
		}
		if changed {
			fmt.Printf("已发布第 %d 版：%s\n", published.Revision, published.Title)
		} else {
			fmt.Printf("没有变化：%s\n", published.Title)
		}
	}
	return nil
}
