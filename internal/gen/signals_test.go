package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSignalRuntime(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, source := range map[string]string{
		"go.mod": "module signals\n\ngo 1.26\n", "scope.go": scopeRuntime,
		"signals_test.go": signalRuntimeTests,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", runtimeTestArgs()...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("signal runtime: %v\n%s", err, out)
	}
}

const signalRuntimeTests = `package main
import (
 "context"
 "strings"
 "syscall"
 "testing"
 "time"
)
type scheduled struct { delay time.Duration; run func() }
func fakeBroker() (*_signalBroker,*time.Time,*[]scheduled,*[]int) {
 b:=_newSignalBroker(false)
 now:=time.Unix(100,0);timers:=[]scheduled{};exits:=[]int{}
 b.now=func()time.Time{return now}
 b.after=func(d time.Duration,f func()){timers=append(timers,scheduled{d,f})}
 b.exit=func(code int){exits=append(exits,code)}
 return b,&now,&timers,&exits
}
func registration(kind string,numbers ...int) *_signalRegistration {
 r:=&_signalRegistration{kind:kind,signals:map[int]bool{},events:make(chan int,16),closed:make(chan struct{})}
 for _,n:=range numbers {r.signals[n]=true};return r
}
func TestCancellationAndEscalation(t *testing.T) {
 for _,n:=range []int{int(syscall.SIGINT),int(syscall.SIGTERM)} {
  b,now,timers,exits:=fakeBroker()
  root:=_scopeWith(b.ctx);other:=_scopeWith(b.ctx);child:=_newScope(root,"nested")
  b.deliver(n)
  for _,s:=range []*_Scope{root,other,child} {if s.ctx.Err()==nil || !strings.Contains(context.Cause(s.ctx).Error(),_borkSignalName(n)) {t.Fatal("missing named cause")}}
  if b.code!=128+n {t.Fatal("wrong exit status")}
  later:=_scopeWith(b.ctx);if later.ctx.Err()==nil {t.Fatal("cancellation did not latch")}
  b.deliver(n);if len(*exits)!=0 {t.Fatal("duplicate forced exit")}
  *now=now.Add(500*time.Millisecond)
  b.deliver(n);if len(*exits)!=1 || (*exits)[0]!=128+n {t.Fatal("second signal did not exit")}
  if len(*timers)!=1 || (*timers)[0].delay!=500*time.Millisecond {t.Fatal("duplicate window")}
  (*timers)[0].run();if len(b.installed)!=0 {t.Fatal("default signals not restored")}
 }
}
func TestPolicyRestorationAndEmptySet(t *testing.T) {
 b,_,_,_:=fakeBroker();hup:=int(syscall.SIGHUP)
 older:=registration("configure",hup);newer:=registration("configure")
 b.add(older);if !b.installed[hup] || b.installed[int(syscall.SIGTERM)] {t.Fatal("configuration not applied")}
 b.add(newer);if len(b.installed)!=0 {t.Fatal("empty cancellation set")}
 b.remove(older);if len(b.installed)!=0 {t.Fatal("removing old policy overwrote new")}
 b.remove(newer);if !b.installed[int(syscall.SIGTERM)] {t.Fatal("default not restored")}
 b.add(older);b.add(newer);b.remove(newer);if !b.installed[hup] {t.Fatal("prior configuration not restored")}
}
func TestSubscriptionsAndIgnore(t *testing.T) {
 b,_,_,_:=fakeBroker();n:=int(syscall.SIGTERM)
 ignore:=registration("ignore",n);first:=registration("subscribe",n);second:=registration("subscribe",n)
 b.add(ignore);b.deliver(n);if b.ctx.Err()!=nil {t.Fatal("ignore cancelled")}
 b.add(first);b.add(second);b.deliver(n)
 if <-first.events!=n || <-second.events!=n || b.ctx.Err()!=nil || b.code!=0 {t.Fatal("subscription precedence/fanout")}
 for i:=0;i<100;i++ {b.deliver(n)};if len(first.events)!=16 {t.Fatal("unbounded events")}
 b.remove(first);select {case <-first.closed:default:t.Fatal("subscription did not close")}
 b.remove(second);b.deliver(n);if b.ctx.Err()!=nil {t.Fatal("ignore not restored")}
 b.remove(ignore);b.deliver(n);if b.ctx.Err()==nil {t.Fatal("cancellation not restored")}
}
func TestGraceLatchesAndKeepsSubscriptions(t *testing.T) {
 b,_,timers,exits:=fakeBroker()
 policy:=registration("configure",int(syscall.SIGTERM));policy.bounded=true;policy.grace=2*time.Second
 events:=registration("subscribe",int(syscall.SIGHUP));b.add(policy);b.add(events)
 b.deliver(int(syscall.SIGTERM));b.remove(policy)
 if len(*timers)!=2 || (*timers)[1].delay!=2*time.Second {t.Fatal("grace not captured")}
 (*timers)[0].run();if !b.installed[int(syscall.SIGHUP)] || b.installed[int(syscall.SIGTERM)] {t.Fatal("subscription lost during shutdown")}
 b.deliver(int(syscall.SIGHUP));if <-events.events!=int(syscall.SIGHUP) {t.Fatal("no reload during cleanup")}
 (*timers)[1].run();if len(*exits)!=1 || (*exits)[0]!=143 {t.Fatal("grace exit status")}
 zero,_,zeroTimers,_:=fakeBroker();immediate:=registration("configure",int(syscall.SIGINT));immediate.bounded=true;zero.add(immediate);zero.deliver(int(syscall.SIGINT))
 if (*zeroTimers)[1].delay!=0 {t.Fatal("zero grace did not expire immediately")}
}
func TestConfiguredUserSignalStillEscalates(t *testing.T) {
 b,now,timers,exits:=fakeBroker()
 n,err:=_borkSignalNumber("User1");if err!=nil {t.Skip(err)}
 b.add(registration("configure",n));b.deliver(n)
 (*timers)[0].run()
 if !b.installed[n] {t.Fatal("nonterminating OS default cannot handle escalation")}
 *now=now.Add(time.Second);b.deliver(n)
 if len(*exits)!=1 || (*exits)[0]!=128+n {t.Fatal("second user signal did not exit")}
}
func TestUnusedScopeRuntimeInstallsNothing(t *testing.T) {
 if _mainSignals.system {t.Fatal("scope runtime eagerly installed handlers")}
}
func TestMockIsolation(t *testing.T) {
 owner:=_scopeWith(context.Background());r:=_borkSignalMock(owner)
 if ok,err:=_borkSignalEmit(r,int(syscall.SIGINT));!ok || err!=nil {t.Fatal("mock emit failed")}
 if <-r.events!=int(syscall.SIGINT) || owner.ctx.Err()!=nil {t.Fatal("mock affected cancellation")}
 _borkSignalClose(r)
 if ok,err:=_borkSignalEmit(r,int(syscall.SIGINT));ok || err!=nil {t.Fatal("closed mock accepted event")}
 if _,err:=_borkSignalEmit(registration("subscribe"),int(syscall.SIGINT));err==nil {t.Fatal("real subscription accepted emit")}
}
`
