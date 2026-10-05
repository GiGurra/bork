package gen

// The broker is part of scopeRuntime so standalone runtime tests retain the
// same root cancellation and signal behavior as generated programs.
const signalRuntime = `

type _signalRegistration struct {
 mu sync.Mutex
 kind string
 signals map[int]bool
 grace time.Duration
 bounded bool
 events chan int
 closed chan struct{}
 mock bool
 once sync.Once
}

type _signalBroker struct {
 mu sync.Mutex
 started sync.Once
 ctx context.Context
 cancel context.CancelCauseFunc
 registrations []*_signalRegistration
 watches map[int]chan os.Signal
 installed map[int]bool
 system bool
 code int
 first time.Time
 presses []time.Time
 released bool
 now func() time.Time
 after func(time.Duration,func())
 exit func(int)
}

func _newSignalBroker(system bool) *_signalBroker {
 ctx,cancel:=context.WithCancelCause(context.Background())
 b:=&_signalBroker{ctx:ctx,cancel:cancel,watches:map[int]chan os.Signal{},installed:map[int]bool{},now:time.Now,after:func(d time.Duration,f func()){time.AfterFunc(d,f)},exit:os.Exit}
 b.refresh()
 if system { b.start() }
 return b
}

// Install lazily at the first root scope, even when an unused imported std
// declaration caused the generator to include scope support.
func (b *_signalBroker) start() {
 b.started.Do(func(){
  b.mu.Lock();b.system=true;b.installed=map[int]bool{};b.refresh();b.mu.Unlock()
 })
}

func _borkSignalNumber(name string) (int,error) {
 switch name {
 case "Interrupt":return int(syscall.SIGINT),nil
 case "Terminate":return int(syscall.SIGTERM),nil
 case "Hangup":if runtime.GOOS!="windows" {return int(syscall.SIGHUP),nil}
 case "User1","User2":
  if runtime.GOOS!="windows" {
   label:="user defined signal 1";if name=="User2" {label="user defined signal 2"}
   // Go's signal-name table follows the target OS and architecture, including
   // Linux MIPS, whose user-signal numbers differ from Linux AMD64.
   alternate:="user Signal 1";if name=="User2" {alternate="user Signal 2"}
   for n:=1;n<128;n++ {if text:=syscall.Signal(n).String();text==label || text==alternate {return n,nil}}
  }
 }
 return 0,fmt.Errorf("signal %s is unsupported on %s",name,runtime.GOOS)
}

func _borkSignalMockNumber(name string) int {
 for i,value:=range []string{"Interrupt","Terminate","Hangup","User1","User2"} {if value==name {return -(i+1)}}
 panic("bork: unknown mock signal")
}
func _borkSignalName(n int) string {
 if n<0 && n>=-5 {return []string{"Interrupt","Terminate","Hangup","User1","User2"}[-n-1]}
 for _,name:=range []string{"Interrupt","Terminate","Hangup","User1","User2"} {
  if number,err:=_borkSignalNumber(name);err==nil && number==n {return name}
 }
 return "unknown signal"
}

func (b *_signalBroker) policy() (map[int]bool,time.Duration,bool) {
 for i:=len(b.registrations)-1;i>=0;i-- {
  r:=b.registrations[i];if r.kind=="configure" {return r.signals,r.grace,r.bounded}
 }
 return map[int]bool{int(syscall.SIGINT):true,int(syscall.SIGTERM):true},0,false
}

// refresh is called under mu, except during construction. Each signal keeps
// its own notification channel so unrelated updates never restore its OS
// default even momentarily. Stop restores Go's saved disposition on removal.
func (b *_signalBroker) refresh() {
 wanted:=map[int]bool{}
 signals,_,_:=b.policy()
 for n:=range signals {
  if !b.released || (n!=int(syscall.SIGINT) && n!=int(syscall.SIGTERM) && n!=int(syscall.SIGHUP)) {wanted[n]=true}
 }
 for _,r:=range b.registrations {if r.kind!="configure" {for n:=range r.signals {wanted[n]=true}}}
 if b.system {
  for n:=range wanted {
   if b.watches[n]!=nil {continue}
   events:=make(chan os.Signal,32)
   signal.Notify(events,syscall.Signal(n))
   b.watches[n]=events
   go func(){for value:=range events {if delivered,ok:=value.(syscall.Signal);ok {b.deliver(int(delivered))}}}()
  }
  for n,events:=range b.watches {
   if wanted[n] {continue}
   signal.Stop(events)
   close(events)
   delete(b.watches,n)
  }
 }
 b.installed=wanted
}

func (b *_signalBroker) add(r *_signalRegistration) {
 b.mu.Lock();defer b.mu.Unlock()
 b.registrations=append(b.registrations,r);b.refresh()
}
func (b *_signalBroker) remove(r *_signalRegistration) {
 b.mu.Lock();defer b.mu.Unlock()
 for i,value:=range b.registrations {if value==r {b.registrations=append(b.registrations[:i],b.registrations[i+1:]...);break}}
 r.once.Do(func(){close(r.closed)})
 b.refresh()
}

// Three Ctrl+C presses within 5 seconds always exit, even when the program
// subscribes to or ignores Interrupt, so the keyboard can stop any program.
// Copies within 500ms of a counted press are the same press.
func (b *_signalBroker) deliver(n int) {
 b.mu.Lock()
 if n==int(syscall.SIGINT) {
  now:=b.now()
  if len(b.presses)==0 || now.Sub(b.presses[len(b.presses)-1])>=500*time.Millisecond {
   kept:=b.presses[:0]
   for _,press:=range b.presses {if now.Sub(press)<5*time.Second {kept=append(kept,press)}}
   b.presses=append(kept,now)
   if len(b.presses)>=3 {b.mu.Unlock();b.exit(128+n);return}
  }
 }
 subscribed,ignored:=false,false
 for _,r:=range b.registrations {
  if !r.signals[n] {continue}
  if r.kind=="subscribe" {subscribed=true;select {case r.events<-n:default:}}
  if r.kind=="ignore" {ignored=true}
 }
 if subscribed || ignored {b.mu.Unlock();return}
 signals,grace,bounded:=b.policy()
 if !signals[n] {b.mu.Unlock();return}
 if b.code!=0 {
  duplicate:=b.now().Sub(b.first)<500*time.Millisecond
  b.mu.Unlock()
  if !duplicate {b.exit(128+n)}
  return
 }
 b.code=128+n;b.first=b.now()
 b.cancel(fmt.Errorf("signal %s (%s)",_borkSignalName(n),syscall.Signal(n)))
 code:=b.code
 b.mu.Unlock()
 b.after(500*time.Millisecond,func(){b.mu.Lock();b.released=true;b.refresh();b.mu.Unlock()})
 if bounded {b.after(grace,func(){b.exit(code)})}
}

func _borkSignalRegister(s *_Scope,kind string,numbers []int,grace time.Duration,bounded bool) *_signalRegistration {
 r:=&_signalRegistration{kind:kind,signals:map[int]bool{},grace:grace,bounded:bounded,events:make(chan int,16),closed:make(chan struct{})}
 for _,n:=range numbers {r.signals[n]=true}
 _mainSignals.add(r)
 return r
}

func _borkSignalMock(s *_Scope) *_signalRegistration {
 r:=&_signalRegistration{mock:true,events:make(chan int,16),closed:make(chan struct{})}
 return r
}
func _borkSignalClose(r *_signalRegistration) {
 if r.mock {r.mu.Lock();r.once.Do(func(){close(r.closed)});r.mu.Unlock()} else {_mainSignals.remove(r)}
}
func _borkSignalEmit(r *_signalRegistration,n int) (bool,error) {
 if !r.mock {return false,errors.New("emit requires a MockSubscription")}
 r.mu.Lock();defer r.mu.Unlock()
 select {case <-r.closed:return false,nil;default:}
 select {case r.events<-n:default:}
 return true,nil
}

func _borkSignalExit() {
 _mainSignals.mu.Lock();code:=_mainSignals.code;_mainSignals.mu.Unlock()
 if code!=0 {os.Exit(code)}
}
`
