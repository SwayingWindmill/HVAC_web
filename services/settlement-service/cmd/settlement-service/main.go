package main

import (
	"context"
	"encoding/json"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/quanlaihe/hvac-web/services/settlement-service/internal/settlement"
)

type request struct { Operation string `json:"operation"`; TenantID string `json:"tenantId"`; SiteID string `json:"siteId"`; PeriodID string `json:"periodId"`; CandidateID string `json:"candidateId"`; Reason string `json:"reason"` }
func main(){
	ctx:=context.Background();pool,err:=pgxpool.New(ctx,os.Getenv("SETTLEMENT_POSTGRES_DSN"));if err!=nil{log.Fatal(err)};defer pool.Close();repo,_:=settlement.NewPostgresStore(pool);facts,err:=settlement.NewClickHouseStore(os.Getenv("SETTLEMENT_CLICKHOUSE_URL"),os.Getenv("SETTLEMENT_CLICKHOUSE_USER"),os.Getenv("SETTLEMENT_CLICKHOUSE_PASSWORD"),nil);if err!=nil{log.Fatal(err)};engine,_:=settlement.New(repo,facts)
	var in request;if err=json.NewDecoder(os.Stdin).Decode(&in);err!=nil{log.Fatal(err)};enc:=json.NewEncoder(os.Stdout)
	switch in.Operation {case "calculate": out,err:=engine.CalculatePeriod(ctx,in.TenantID,in.SiteID,in.PeriodID);if err!=nil{log.Fatal(err)};_ = enc.Encode(out);case "reconcile": id,err:=engine.ReconcilePeriod(ctx,in.TenantID,in.SiteID,in.PeriodID,in.Reason);if err!=nil{log.Fatal(err)};_ = enc.Encode(map[string]string{"candidateId":id});case "approve": if err:=engine.ApproveCandidate(ctx,in.TenantID,in.SiteID,in.CandidateID);err!=nil{log.Fatal(err)};_ = enc.Encode(map[string]string{"candidateId":in.CandidateID,"status":"APPROVED"});case "apply": out,err:=engine.ApplyApprovedRevision(ctx,in.TenantID,in.SiteID,in.CandidateID);if err!=nil{log.Fatal(err)};_ = enc.Encode(out);default: log.Fatal("unsupported settlement operation")}
}
