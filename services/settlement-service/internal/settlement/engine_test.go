package settlement

import (
	"context"
	"testing"
	"time"
)

type fakeRepo struct{ period Period; tariff Tariff; meters []MeterBinding; snapshot Snapshot; candidate Candidate; transitions []string }
func (f *fakeRepo) LoadPeriod(context.Context,string,string,string)(Period,[]MeterBinding,Tariff,error){return f.period,f.meters,f.tariff,nil}
func (f *fakeRepo) TransitionPeriod(_ context.Context,_ Period,status string,_ time.Time)error{f.transitions=append(f.transitions,status);return nil}
func (f *fakeRepo) InsertSnapshot(_ context.Context,s Snapshot)error{f.snapshot=s;return nil}
func (f *fakeRepo) LatestSnapshot(context.Context,string,string,string)(Snapshot,error){return f.snapshot,nil}
func (f *fakeRepo) CreateCandidate(_ context.Context,c Candidate,_ time.Time)error{f.candidate=c;return nil}
func (f *fakeRepo) ApproveCandidate(context.Context,string,string,string,time.Time)error{return nil}
func (f *fakeRepo) LoadApprovedCandidate(context.Context,string,string,string)(Candidate,Snapshot,error){return f.candidate,f.snapshot,nil}
func (f *fakeRepo) ApplyRevision(_ context.Context,_ Candidate,_ Snapshot,next Snapshot,_ time.Time)error{f.snapshot=next;return nil}
type fakeFacts struct{ facts []Fact }
func (f *fakeFacts) ReadEnergyFacts(context.Context,Period,[]MeterBinding)([]Fact,error){return f.facts,nil}

func TestCalculatePeriod(t *testing.T){
	start:=time.Date(2026,8,3,0,0,0,0,time.UTC)
	r:=&fakeRepo{period:Period{TenantID:"t",SiteID:"s",ID:"p",BoundaryID:"b",Timezone:"UTC",Status:"OPEN",Start:start,End:start.Add(time.Hour)},meters:[]MeterBinding{{ID:"mb",PointID:"pt"}},tariff:Tariff{VersionID:"tv",Currency:"CNY",Periods:[]TariffPeriod{{Code:"FLAT",DayType:"WEEKDAY",StartMinute:0,EndMinute:1440,EnergyRate:.5,DemandRate:2}}}}
	f:=&fakeFacts{facts:[]Fact{{ID:"fact",PointID:"pt",Start:start,End:start.Add(time.Hour),KWh:10,Quality:"GOOD"}}};e,_:=New(r,f);got,err:=e.CalculatePeriod(context.Background(),"t","s","p");if err!=nil{t.Fatal(err)};if got.Calculation.TotalCost!=25{t.Fatalf("cost=%v",got.Calculation.TotalCost)};if got.Calculation.Quality!="GOOD"{t.Fatalf("quality=%s",got.Calculation.Quality)};if len(r.transitions)!=2||r.transitions[0]!="CALCULATING"||r.transitions[1]!="REVIEW"{t.Fatalf("transitions=%v",r.transitions)}
}
