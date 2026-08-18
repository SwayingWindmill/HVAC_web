package metric

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ClickHouseStore struct { base *url.URL; user,password string; client *http.Client }
func NewClickHouseStore(baseURL,user,password string, client *http.Client)(*ClickHouseStore,error){
	u,err:=url.Parse(baseURL);if err!=nil||u.Host==""||(u.Scheme!="http"&&u.Scheme!="https"){return nil,errors.New("metric ClickHouse URL must be absolute http/https")};if client==nil{client=&http.Client{Timeout:15*time.Second}};return &ClickHouseStore{base:u,user:user,password:password,client:client},nil
}

func (s *ClickHouseStore) Ping(ctx context.Context) error { _, err := s.query(ctx, "SELECT 1"); return err }

func (s *ClickHouseStore) query(ctx context.Context, sql string)([]byte,error){u:=*s.base;q:=u.Query();q.Set("query",sql);q.Set("date_time_output_format","iso");u.RawQuery=q.Encode();req,err:=http.NewRequestWithContext(ctx,http.MethodGet,u.String(),nil);if err!=nil{return nil,err};if s.user!=""{req.SetBasicAuth(s.user,s.password)};resp,err:=s.client.Do(req);if err!=nil{return nil,err};defer resp.Body.Close();body,err:=io.ReadAll(io.LimitReader(resp.Body,1<<20));if err!=nil{return nil,err};if resp.StatusCode/100!=2{return nil,fmt.Errorf("metric ClickHouse query returned %d: %s",resp.StatusCode,strings.TrimSpace(string(body)))};return body,nil}

func quote(value string) string { return "'"+strings.ReplaceAll(value,"'","''")+"'" }
func aggregateExpr(name string) string { switch strings.ToUpper(strings.TrimSpace(name)){case "SUM":return "sum(value_number)";case "MIN":return "min(value_number)";case "MAX":return "max(value_number)";case "FIRST":return "argMin(value_number,sampled_at)";case "LAST":return "argMax(value_number,sampled_at)";default:return "avg(value_number)"} }

func (s *ClickHouseStore) ReadPoint(ctx context.Context,b Binding,pointID string,start,end time.Time)(Input,error){
	sql:=fmt.Sprintf(`SELECT %s AS value, count() AS n, countIf(quality='GOOD') AS good, argMin(value_number,sampled_at) AS first_value, argMax(value_number,sampled_at) AS last_value, dateDiff('millisecond',min(sampled_at),max(sampled_at))/1000.0 AS duration_seconds, avg(value_number)*dateDiff('millisecond',min(sampled_at),max(sampled_at))/3600000.0 AS integral FROM telemetry_history.observations WHERE tenant_id=toUUID(%s) AND site_id=toUUID(%s) AND point_id=toUUID(%s) AND acceptance_status IN ('ACCEPTED','OUT_OF_ORDER') AND value_number IS NOT NULL AND sampled_at >= parseDateTime64BestEffort(%s) AND sampled_at < parseDateTime64BestEffort(%s) FORMAT TabSeparated`,aggregateExpr(b.Aggregation),quote(b.TenantID),quote(b.SiteID),quote(pointID),quote(start.UTC().Format(time.RFC3339Nano)),quote(end.UTC().Format(time.RFC3339Nano)))
	body,err:=s.query(ctx,sql);if err!=nil{return Input{},err};parts:=strings.Split(strings.TrimSpace(string(body)),"\t");if len(parts)!=7||parts[0]=="nan"||parts[0]==""{return Input{},errors.New("metric point dependency has no numeric observations")};v,err:=strconv.ParseFloat(parts[0],64);if err!=nil{return Input{},err};n,err:=strconv.ParseInt(parts[1],10,64);if err!=nil||n<=0{return Input{},errors.New("metric point dependency is empty")};good,_:=strconv.ParseFloat(parts[2],64);first,err:=strconv.ParseFloat(parts[3],64);if err!=nil{return Input{},err};last,err:=strconv.ParseFloat(parts[4],64);if err!=nil{return Input{},err};duration,_:=strconv.ParseFloat(parts[5],64);integral,_:=strconv.ParseFloat(parts[6],64);q:="GOOD";c:=1.0;if good<float64(n){q="PARTIAL";c=good/float64(n)};return Input{Reference:"point:"+pointID,Value:v,FirstValue:first,LastValue:last,Count:n,DurationSeconds:duration,Integral:integral,Quality:q,Completeness:c},nil
}
func (s *ClickHouseStore) ReadMetric(ctx context.Context,b Binding,d Dependency,start,end time.Time)(Input,error){
	sql:=fmt.Sprintf(`SELECT value_number,quality,completeness,result_id FROM analytics.metric_series WHERE tenant_id=toUUID(%s) AND site_id=toUUID(%s) AND metric_id=toUUID(%s) AND subject_type=%s AND subject_id=toUUID(%s) AND period_start=%s AND period_end=%s ORDER BY revision DESC,calculated_at DESC LIMIT 1 FORMAT TabSeparated`,quote(b.TenantID),quote(b.SiteID),quote(d.MetricID),quote(b.SubjectType),quote(b.SubjectID),quote(start.UTC().Format("2006-01-02 15:04:05.000")),quote(end.UTC().Format("2006-01-02 15:04:05.000")))
	body,err:=s.query(ctx,sql);if err!=nil{return Input{},err};parts:=strings.Split(strings.TrimSpace(string(body)),"\t");if len(parts)!=4||parts[0]=="\\N"||parts[0]==""{return Input{},errors.New("metric dependency result is unavailable")};v,err:=strconv.ParseFloat(parts[0],64);if err!=nil{return Input{},err};c,_:=strconv.ParseFloat(parts[2],64);return Input{Reference:"metric-result:"+parts[3],Value:v,FirstValue:v,LastValue:v,Count:1,DurationSeconds:end.Sub(start).Seconds(),Integral:v*end.Sub(start).Hours(),Quality:parts[1],Completeness:c},nil
}
func (s *ClickHouseStore) HasMetricResult(ctx context.Context,resultID string)(bool,error){
	body,err:=s.query(ctx,fmt.Sprintf(`SELECT count() FROM analytics.metric_series WHERE result_id=toUUID(%s) FORMAT TabSeparated`,quote(resultID)));if err!=nil{return false,err};count,err:=strconv.ParseInt(strings.TrimSpace(string(body)),10,64);if err!=nil{return false,err};return count>0,nil
}

func (s *ClickHouseStore) InsertMetric(ctx context.Context,r Result)error{
	provenance,_:=json.Marshal(map[string]any{"calculationRunId":r.RunID,"inputs":SortedInputs(r.Inputs)})
	row:=map[string]any{"result_id":r.ResultID,"tenant_id":r.Binding.TenantID,"site_id":r.Binding.SiteID,"subject_type":r.Binding.SubjectType,"subject_id":r.Binding.SubjectID,"metric_id":r.Binding.MetricID,"metric_version_id":r.Binding.MetricVersionID,"metric_code":r.Binding.MetricCode,"metric_version":r.Binding.MetricVersion,"metric_binding_id":r.Binding.BindingID,"binding_version":r.Binding.BindingVersion,"period_start":r.PeriodStart,"period_end":r.PeriodEnd,"calculated_at":r.CalculatedAt,"granularity":r.Binding.Granularity,"value_type":"NUMBER","value_json":strconv.FormatFloat(r.Value,'g',-1,64),"value_number":r.Value,"unit":r.Binding.Unit,"quality":r.Quality,"completeness":r.Completeness,"calculation_run_id":r.RunID,"revision":uint64(1),"provenance":string(provenance)}
	var body bytes.Buffer;enc:=json.NewEncoder(&body);enc.SetEscapeHTML(false);if err:=enc.Encode(row);err!=nil{return err};u:=*s.base;q:=u.Query();q.Set("database","analytics");q.Set("query","INSERT INTO analytics.metric_series FORMAT JSONEachRow");q.Set("date_time_input_format","best_effort");u.RawQuery=q.Encode();req,err:=http.NewRequestWithContext(ctx,http.MethodPost,u.String(),&body);if err!=nil{return err};req.Header.Set("Content-Type","application/x-ndjson");if s.user!=""{req.SetBasicAuth(s.user,s.password)};resp,err:=s.client.Do(req);if err!=nil{return err};defer resp.Body.Close();if resp.StatusCode/100!=2{detail,_:=io.ReadAll(io.LimitReader(resp.Body,2048));return fmt.Errorf("insert metric result returned %d: %s",resp.StatusCode,strings.TrimSpace(string(detail)))};return nil
}

