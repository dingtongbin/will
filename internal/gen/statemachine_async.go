package gen

import "github.com/will-lang/will/internal/willc"

// asyncField is one parameter or local stored in an async state-machine frame.
// The frame keeps values that must survive a suspension; the step function
// addresses them through GEPs instead of stack slots.
type asyncField struct {
	id        int
	text      string
	willText  string
	llvm      string
	aggregate bool
	param     bool
	paramMut  bool
}

// asyncMachineInfo describes one function whose only wait is a direct async C
// call. The frame is state, operation handle, parameters, and locals. The
// constructor starts a task; the step starts the operation once and polls it
// after each wakeup.
// asyncSegment is one suspension in a sequence. before runs immediately before
// the operation starts; after a child call the call's own statement still runs
// so its ?/binding sees the stored result.
type asyncSegment struct {
	call        willc.Record
	kind        string
	startName   string
	pollName    string
	finishName  string
	deadline    string
	targetSym   string
	statusIndex int
	resultIndex int
	before      []willc.Record
	stmt        willc.Record
}

type asyncMachineInfo struct {
	fn          willc.Record
	symbol      string
	constructor string
	frameType   string
	fields      []asyncField
	resultText  string
	resultLLVM  string
	segments    []asyncSegment
	tail        []willc.Record
}

// asyncStartName is the C entry that submits one asynchronous operation. The
// blocking form stays available for callers outside a state machine.
func asyncStartName(name string) string {
	return name + "_start"
}

// asyncOps names the poll, finish, and deadline entries of operations whose
// wait is a runtime service rather than a blocking-pool job. Sleep waits
// through the task's park timer; the poller delivers readiness to the parked
// task through its embedded request. Both carry an absolute deadline that the
// next park arms.
var asyncOps = map[string][3]string{
	"willrt_task_sleep_async": {"willrt_task_sleep_poll", "willrt_task_sleep_finish", "willrt_task_sleep_deadline"},
	"willrt_poll_wait_async":  {"willrt_poll_wait_async_poll", "willrt_poll_wait_async_finish", "willrt_poll_wait_async_deadline"},
	// A sync wait registers the task's embedded node and parks until the
	// primitive grants it, the caller's deadline passes, or it is canceled.
	"willrt_sync_lock_async":         {"willrt_sync_wait_poll", "willrt_sync_wait_finish", "willrt_sync_wait_deadline"},
	"willrt_sync_sema_acquire_async": {"willrt_sync_wait_poll", "willrt_sync_wait_finish", "willrt_sync_wait_deadline"},
	"willrt_sync_count_wait_async":   {"willrt_sync_wait_poll", "willrt_sync_wait_finish", "willrt_sync_wait_deadline"},
	"willrt_sync_gate_wait_async":    {"willrt_sync_wait_poll", "willrt_sync_wait_finish", "willrt_sync_wait_deadline"},
	// The whole-file operations submit to the completion engine and park
	// until the disk, the scope deadline, or cancellation decides.
	"willrt_fs_async_read_file":  {"willrt_io_poll", "willrt_io_finish", "willrt_io_deadline"},
	"willrt_fs_async_write_file": {"willrt_io_poll", "willrt_io_finish", "willrt_io_deadline"},
	"willrt_fs_async_append":     {"willrt_io_poll", "willrt_io_finish", "willrt_io_deadline"},
}

func callTargetSymbolFor(d *docState, call willc.Record) string {
	if q := textField(call, "to"); q != "" {
		return q
	}
	return d.symbols[refField(call, "to")]
}

func (g *generator) asyncFieldOf(r willc.Record, param bool) (asyncField, bool) {
	llvm, err := g.declLLVMType(r.ID)
	if err != nil || llvm == "" {
		return asyncField{}, false
	}
	willText, err := g.willTypeText(r.ID)
	if err != nil {
		return asyncField{}, false
	}
	return asyncField{
		id:        r.ID,
		text:      fieldText(r, "text"),
		willText:  willText,
		llvm:      llvm,
		aggregate: isAggregateType(llvm),
		param:     param,
		paramMut:  isMutText(fieldText(r, "type")),
	}, true
}
