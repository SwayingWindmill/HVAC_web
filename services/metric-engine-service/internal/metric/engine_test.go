package metric

import (
	"context"
	"testing"
	"time"
)

type fakeRegistry struct{ b Binding; status []string }
func (f *fakeRegistry) LoadBinding(context.Context,string,string,string,time.Time)(Binding,error){return f.b,nil}
func (f *fakeRegistry) CreateRun(context.Context,Result,string,[]byte)error{f.status=append(f.status,"PENDING");return nil}
func (f *fakeRegistry) MarkRunRunning(context.Context,string,string,string,time.Time)error{f.status=append(f.status,"RUNNING");return nil}
func (f *fakeRegistry) MarkRunCompleted(_ context.Context,_,_,_,status string,_ time.Time)error{f.status=append(f.status,status);return nil}
type fakeSeries struct{ inserted Result }
func (f *fakeSeries) ReadPoint(context.Context,Binding,string,time.Time,time.Time)(Input,error){return Input{Reference:"point:p",Value:20,Quality:"GOOD",Completeness:1},nil}
func (f *fakeSeries) ReadMetric(context.Context,Binding,Dependency,time.Time,time.Time)(Input,error){return Input{Reference:"metric:m",Value:10,Quality:"GOOD",Completeness:1},nil}
func (f *fakeSeries) InsertMetric(_ context.Context,r Result)error{f.inserted=r;return nil}
func TestExecuteMetric(t *testing.T){
	r:=&fakeRegistry{b:Binding{TenantID:"t",SiteID:"s",BindingID:"b",MetricVersionID:"v",MetricID:"m",MetricCode:"efficiency",MetricVersion:1,BindingVersion:1,SubjectType:"SITE",SubjectID:"s",Granularity:"HOUR",DataType:"NUMBER",CalculationMethod:"RATIO",QualityPolicy:"STRICT",SourceDefinition:map[string]any{"points":map[string]any{"load":"p"}},Dependencies:[]Dependency{{Type:"POINT",Code:"load",Required:true},{Type:"METRIC",Code:"baseline",MetricID:"bm",Required:true}}}}
	s:=&fakeSeries{};e,_:=New(r,s);start:=time.Date(2026,8,1,0,0,0,0,time.UTC);got,err:=e.Execute(context.Background(),RunRequest{TenantID:"t",SiteID:"s",BindingID:"b",PeriodStart:start,PeriodEnd:start.Add(time.Hour)});if err!=nil{t.Fatal(err)};if got.Value!=2{t.Fatalf("value=%v",got.Value)};if len(r.status)!=3||r.status[2]!="SUCCEEDED"{t.Fatalf("status=%v",r.status)}
}
