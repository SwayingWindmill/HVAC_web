// Publishes a Site's alarm rules file (deploy/sites/<site>.alarm-rules.json) as the whole set of
// rules its Site evaluates (#609, #693). A rule whose content is unchanged is left alone, so running
// it again changes nothing; a changed rule becomes the next revision of its family; a rule on the
// Site that the file no longer has (deleted, renamed, or its device lost its last active point) is
// retired and its active Alarm recovers.
//
//	ALARM_DATABASE_URL_FILE=<file holding the Alarm database URL> alarm-rules [-print] <rules file>
//
// -print lists what publishing would add, change and retire, and writes nothing.
//
// The Alarm database is reachable only from the application network; run it with the Alarm
// owner's identity, e.g. `docker run --rm --network container:<energy-api> --volumes-from
// <energy-api> -e ALARM_DATABASE_URL_FILE=… -v <dir>:/alarm-rules:ro --entrypoint
// /alarm-rules/alarm-rules <energy-api image> /alarm-rules/<site>.alarm-rules.json`.
package main

import (
	"context"
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
	printOnly := flag.Bool("print", false, "list what publishing would change, without writing")
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

	active, err := store.SiteRules(ctx, compiled.TenantID, compiled.SiteID)
	if err != nil {
		return err
	}
	plan, err := alarmservice.PlanSiteRules(compiled.Policies, active)
	if err != nil {
		return err
	}
	for _, group := range []struct {
		label string
		rules []alarmservice.SiteRule
	}{{"新增", plan.Added}, {"更新", plan.Changed}, {"撤销", plan.Retired}} {
		for _, rule := range group.rules {
			fmt.Println(group.label + "：" + rule.Policy.Title)
		}
	}
	fmt.Println("没有变化的规则", len(plan.Unchanged), "条")
	if printOnly {
		return nil
	}

	publishedBy := "alarm-rules:" + filepath.Base(rulesPath)
	now := time.Now().UTC()
	for _, sitePolicy := range compiled.Policies {
		published, changed, err := store.PublishSitePolicy(ctx, compiled.TenantID, compiled.SiteID, sitePolicy.Policy, sitePolicy.AssignmentID, now, publishedBy)
		if err != nil {
			return fmt.Errorf("%s: %w", sitePolicy.Policy.Title, err)
		}
		if changed {
			fmt.Println("已发布第", published.Revision, "版：", published.Title)
		}
	}
	for _, rule := range plan.Retired {
		if err := store.RetireSiteRule(ctx, compiled.TenantID, compiled.SiteID, rule.AssignmentID, now, publishedBy); err != nil {
			return fmt.Errorf("撤销 %s: %w", rule.Policy.Title, err)
		}
		fmt.Println("已撤销：" + rule.Policy.Title)
	}
	return nil
}
