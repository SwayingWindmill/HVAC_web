package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/services/rule-runtime-service/pkg/rulemanagement"
)

func loadRuleManagement(ctx context.Context) (*rulemanagement.Manager, func(), error) {
	databaseURL := strings.TrimSpace(os.Getenv("RULE_RUNTIME_DATABASE_URL"))
	if databaseURL == "" {
		return nil, func() {}, nil
	}
	if len(databaseURL) > 64<<10 {
		return nil, func() {}, errors.New("Rule Runtime database URL is invalid")
	}
	openContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	store, err := rulemanagement.OpenPostgresStore(openContext, databaseURL)
	if err != nil {
		return nil, func() {}, err
	}
	manager, err := rulemanagement.NewManager(store, time.Now)
	if err != nil {
		store.Close()
		return nil, func() {}, err
	}
	return manager, store.Close, nil
}
