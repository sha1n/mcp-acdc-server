# Detaching a long-lived shared server process on macOS, Linux and Windows

Research for [#141](https://github.com/sha1n/mcp-acdc-server/issues/141), part of the singleton-mode map [#139](https://github.com/sha1n/mcp-acdc-server/issues/139).
Researched 2026-11-13. Sources were read at the commits linked below; closed-source hosts are called out where their behaviour could not be verified.

Labels used throughout:

- **Verified**: read directly in the cited primary source.
- **Inference**: follows from verified facts but was not itself observed or documented.
- **Unverified**: no primary source found.

Nothing in this document was tested empirically. No process was spawned under any host.

## Summary

1. On macOS and Linux a Go process can start a shared server that outlives it by re-executing its own binary with `SysProcAttr{Setsid: true}` and stdio on the null device. This escapes every termination mechanism found in the verified hosts, with one exception: a parent-PID tree walk (VS Code), which only fires when the session proxy fails to exit in time. An intermediate launcher process that exits immediately closes that gap.
2. On Windows, `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP` is enough under Node/libuv-based hosts, but **not** under hosts that place the server in a kill-on-close job object without breakaway. Codex and every host built on the official Python SDK do exactly that, and they terminate the job on every shutdown, including clean ones. A process created with `CreateProcess` cannot leave such a job.
3. Hosts differ in the first shutdown step. The spec says close stdin first. Claude Code on macOS and Linux sends `SIGINT` first and reaches `SIGKILL` 500 ms later. A session proxy must therefore exit promptly on stdin EOF, `SIGINT` and `SIGTERM`.
4. Containment that no user-space flag escapes: a systemd unit or scope being stopped (`KillMode=control-group` is the default), logout with `KillUserProcesses=yes`, a container whose PID 1 exits, and a Windows job without breakaway.
5. Recommendation: **fall back to the per-session server on Windows**. Parity is not achievable at reasonable cost. Details in [Windows: parity or fallback](#windows-parity-or-fallback).

## 1. Mechanisms per OS

### Constraint common to all platforms: re-exec, not fork

Go cannot daemonize in-process. The runtime is multi-threaded, and the code that runs between `fork` and `exec` is restricted to raw syscalls: "In the child, this function must not acquire any locks, because they might have been locked at the time of the fork. This means no rescheduling, no malloc calls, and no new stack segments" (**verified**, `syscall/exec_libc2.go` and `syscall/exec_bsd.go`, Go 1.27.3, comment above `forkAndExecInChild`). The only supported route is to start a new process, in practice the same binary located with `os.Executable()` and run in a "shared server" mode.

Relevant `os/exec` behaviour (**verified**, `os/exec/exec.go`, Go 1.27.3, <https://pkg.go.dev/os/exec#Cmd>):

- "If Stdin is nil, the process reads from the null device (os.DevNull)." The same holds for `Stdout` and `Stderr`. Leaving all three nil is what disconnects the child from the host's pipes.
- If any of them is not an `*os.File`, `os/exec` creates a pipe and a copying goroutine, which ties the child's stdio to the parent's lifetime. A detached child must use nil or a real file.
- `Process.Release` "only needs to be called if Process.Wait is not" (**verified**, `os/exec.go`). The parent should call `Release` after `Start` rather than `Wait`.
- `exec.CommandContext` kills the child when the context is done. The detached spawn must use `exec.Command`, or a context that is never cancelled.

### macOS and Linux

`syscall.SysProcAttr` on both platforms exposes (**verified**, `syscall/exec_libc2.go` for darwin, `syscall/exec_linux.go` for linux, <https://pkg.go.dev/syscall#SysProcAttr>):

| Field | Effect |
| --- | --- |
| `Setsid bool` | "Create session." Calls `setsid(2)` in the child before `exec`. |
| `Setpgid bool`, `Pgid int` | "sets the process group ID of the child to Pgid, or, if Pgid == 0, to the new child's process ID." |
| `Noctty bool` | "Detach fd 0 from controlling terminal". |
| `Pdeathsig Signal` (Linux only) | Signal the kernel sends to the child when the parent thread dies. Must stay zero for a detached child. |

`setsid(2)` (**verified**, <https://man7.org/linux/man-pages/man2/setsid.2.html>): it "creates a new session if the calling process is not a process group leader. The calling process also becomes the process group leader of a new process group in the session. Initially, the new session has no controlling terminal." It fails with `EPERM` "if the calling process is already a process group leader". A freshly forked child is never a group leader, so `Setsid: true` in `SysProcAttr` cannot hit this error; combining it with `Setpgid: true` is redundant.

What `Setsid` buys:

- A new process group, so `killpg` on the session proxy's group does not reach the shared server. This is the mechanism the Python SDK and Codex use (section 2).
- A new session with no controlling terminal, so terminal-generated `SIGINT` (Ctrl-C to the foreground process group) and `SIGHUP` on terminal close do not reach it (**inference** from POSIX session semantics; relevant when the host is a terminal program such as Claude Code or Codex).

`Setpgid` alone gives the first property but not the second.

What `Setsid` does not buy:

- The parent PID is unchanged. While the session proxy is alive, the shared server is still its child, and tools that walk the tree by parent PID (`pgrep -P`) find it. After the proxy exits the server is re-parented to PID 1 or to the nearest sub-reaper (**verified** for Linux, <https://man7.org/linux/man-pages/man7/pid_namespaces.7.html>: "When a child process becomes orphaned, it is reparented to the 'init' process in the PID namespace of its parent", unless an ancestor used `PR_SET_CHILD_SUBREAPER`).
- cgroup membership is unchanged (section 3).

Closing the parent-PID gap: start the shared server through a short-lived intermediate process (session proxy -> launcher -> shared server, the launcher exits at once). The shared server is then orphaned from the start and is never a descendant of the session proxy in a parent-PID walk (**inference**; this is the classic double-fork expressed as two `exec`s).

Failure modes:

- **Inherited descriptors.** Go opens files close-on-exec and passes only `ProcAttr.Files` to the child, so the host's pipes are not inherited when stdio is nil (**inference** from `os/exec` and `syscall` behaviour; no leak path was found, none was tested). If the shared server did hold the host's stdout pipe, hosts that wait for pipe EOF would hang: the TypeScript SDK v1.x `close()` awaits the Node `close` event, which fires only after stdio streams end (**verified**, section 2), and VS Code, Codex and the Python SDK all carry code for "a kill survivor can hold the stdout pipe open".
- **Zombie.** If the session proxy neither waits nor exits, the shared server becomes a zombie when it dies. `Release` does not reap on Unix. The launcher indirection removes this as well, because PID 1 reaps (**inference**).
- **`os.Executable()` after an upgrade.** The path can point to a replaced or deleted binary. This interacts with the fixed decision that a version mismatch gets its own shared server (**inference**).
- **Working directory and environment.** The shared server inherits both from the first session proxy that starts it. Anything that belongs to server identity must be passed explicitly (**inference**).

### Windows

`syscall.SysProcAttr` on Windows (**verified**, `syscall/exec_windows.go`, Go 1.27.3):

| Field | Use |
| --- | --- |
| `CreationFlags uint32` | OR-ed into the `CreateProcess` flags. |
| `HideWindow bool` | Hides the window. |
| `NoInheritHandles bool` | "no handles are inherited by the new process, not even the standard handles". |
| `ParentProcess Handle` | "the new process regards the process given by this handle as its parent process". |

Go always passes the standard handles through `PROC_THREAD_ATTRIBUTE_HANDLE_LIST`, so only the listed handles are inherited (**verified**, same file). With nil stdio these are handles to `NUL`.

Flag constants: `syscall` defines only `CREATE_NEW_PROCESS_GROUP` (`syscall/types_windows.go`). `DETACHED_PROCESS`, `CREATE_NO_WINDOW` and `CREATE_BREAKAWAY_FROM_JOB` come from `golang.org/x/sys/windows`, which is already an indirect dependency of this module (`go.mod`, `golang.org/x/sys v0.49.0`) and would become a direct one (**verified**).

Flag semantics (**verified**, <https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags>):

- `DETACHED_PROCESS` (0x00000008): "For console processes, the new process does not inherit its parent's console". Cannot be combined with `CREATE_NEW_CONSOLE`.
- `CREATE_NEW_PROCESS_GROUP` (0x00000200): the new process is the root of a new process group, used by `GenerateConsoleCtrlEvent`; "CTRL+C signals will be disabled for all processes within the new process group."
- `CREATE_NO_WINDOW` (0x08000000): console application without a console window; "ignored ... if it is used with either CREATE_NEW_CONSOLE or DETACHED_PROCESS."
- `CREATE_BREAKAWAY_FROM_JOB` (0x01000000): "The child processes of a process associated with a job are not associated with the job. If the calling process is not associated with a job, this constant has no effect. If the calling process is associated with a job, the job must set the JOB_OBJECT_LIMIT_BREAKAWAY_OK limit."

Windows has no parent/child lifetime coupling and no `SIGHUP`. A child survives its parent by default. The two things that kill it are the console (closed console window, Ctrl-C) and job objects. `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP` handles the console. This is the same pair libuv uses for Node's `detached: true` (**verified**, `src/win/process.c` at <https://github.com/libuv/libuv/blob/3280c1c1aab3ad10ab1201c6bfbce4eab022418c/src/win/process.c>).

Job objects (**verified**, <https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects>):

- "After a process is associated with a job, the association cannot be broken."
- "After a process is associated with a job, by default any child processes it creates using CreateProcess are also associated with the job. (Child processes created using Win32_Process.Create are not associated with the job.)"
- Breakaway is possible only if the job allows it: `JOB_OBJECT_LIMIT_BREAKAWAY_OK` together with `CREATE_BREAKAWAY_FROM_JOB` on the child, or `JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK`, under which children are never associated.
- "if the job has the JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE flag specified, closing the last job object handle terminates all associated processes and then destroys the job object itself."
- A host can "Prevent breakaways of any kind by setting neither" breakaway limit.

Failure modes:

- **Job without breakaway.** The shared server is a job member for life and dies when the host terminates or closes the job. Passing `CREATE_BREAKAWAY_FROM_JOB` in that situation makes `CreateProcess` fail (**verified** as a libuv source comment: "CREATE_BREAKAWAY_FROM_JOB makes the CreateProcess call fail if we're under job control that doesn't allow breakaway"; the exact error code was not verified).
- **Routes out of such a job.** `Win32_Process.Create` over WMI is documented as not associated with the job. `PROC_THREAD_ATTRIBUTE_PARENT_PROCESS` (Go's `SysProcAttr.ParentProcess`) takes the job from the nominated parent: "Attributes inherited from the specified process include handles, the device map, processor affinity, priority, quotas, the process token, and job object" (**verified**, <https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-updateprocthreadattribute>). It needs a `PROCESS_CREATE_PROCESS` handle on some unrelated same-user process. Both are techniques security products associate with evasion (**inference**), and neither is a reasonable foundation for a default-on feature.
- **Parent-PID tree kill.** `taskkill /T` "Ends the specified process and any child processes started by it" (**verified**, <https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/taskkill>). Whether it still finds a grandchild whose intermediate parent has exited is **unverified**.
- **No signals.** There is no graceful stop for a windowless detached process other than an application-level request over its IPC channel (**inference**; `GenerateConsoleCtrlEvent` requires a shared console).

## 2. How MCP hosts terminate stdio servers

### What the specification asks for

**Verified**, MCP specification 2025-11-25, Lifecycle, Shutdown, stdio (<https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle#shutdown>, source `docs/specification/2025-11-25/basic/lifecycle.mdx` at <https://github.com/modelcontextprotocol/modelcontextprotocol/blob/06e923405df3df242f1471b9300d9db90ebd10fd/docs/specification/2025-11-25/basic/lifecycle.mdx>):

> the client **SHOULD** initiate shutdown by:
> 1. First, closing the input stream to the child process (the server)
> 2. Waiting for the server to exit, or sending `SIGTERM` if the server does not exit within a reasonable time
> 3. Sending `SIGKILL` if the server does not exit within a reasonable time after `SIGTERM`

It is a SHOULD, names no timeouts, and says nothing about process groups, trees or Windows.

### Verified implementations

| Implementation | Spawn | Shutdown sequence | Scope of the kill |
| --- | --- | --- | --- |
| TypeScript SDK `StdioClientTransport` (v1.x and main) | `cross-spawn`, `shell: false`, `windowsHide` on Windows, not `detached` | `stdin.end()`, wait 2 s, `kill('SIGTERM')`, wait 2 s, `kill('SIGKILL')` | The server process only |
| Python SDK `stdio_client` | POSIX: `start_new_session=True`. Windows: `CREATE_NO_WINDOW`, then assigned to a job with `KILL_ON_JOB_CLOSE` and no breakaway limit | Close stdin, wait 2 s, then kill the tree. The job handle is closed on every shutdown | POSIX: `killpg` on the server's process group. Windows: the whole job |
| Go SDK `CommandTransport` | `exec.Cmd` supplied by the caller | Close stdin, wait, `SIGTERM`, wait, `Kill` | The server process only |
| VS Code | Node `child_process.spawn`, `stdio: 'pipe'`, not `detached` | `stdin.end()`, wait 10 s, polite tree kill (skipped on Windows), wait 10 s, forceful tree kill | Parent-PID tree: `pgrep -P` recursion on POSIX, `taskkill /T /F` on Windows |
| Codex | Unix: new process group. Windows: created suspended and assigned to a job with `KILL_ON_JOB_CLOSE` and **no** breakaway | Terminates on transport close and on drop. Unix: `SIGTERM` to the group, 2 s grace, `SIGKILL` to the group. Windows: terminate the job | Unix: process group. Windows: the whole job |
| Claude Code | Not documented | macOS/Linux: `SIGINT`, `SIGTERM` after 100 ms, `SIGKILL` after a further 400 ms. Windows: close stdin, then end "the server's process tree" after 300 ms | macOS/Linux: "the server process"; group or single PID not documented. Windows: "process tree"; mechanism not documented |

Sources:

- TypeScript SDK: `src/client/stdio.ts` on v1.x, <https://github.com/modelcontextprotocol/typescript-sdk/blob/3b73021a0a2272c0c04c3917bcdb03398b3ec63f/src/client/stdio.ts> (spawn around lines 129-139, `close()` around lines 218-252); `packages/client/src/client/stdio.ts` on main, <https://github.com/modelcontextprotocol/typescript-sdk/blob/919bb8ea88b6e10e72c2f8b6218fad4cfdc40b2c/packages/client/src/client/stdio.ts>.
- Node semantics behind it: `subprocess.kill()` on Windows terminates "forcefully and abruptly" for `SIGTERM`, `SIGKILL`, `SIGINT` and `SIGQUIT` (<https://nodejs.org/api/child_process.html#subprocesskillsignal>), implemented as `TerminateProcess` on the one process handle (libuv `uv__kill`, same `process.c`).
- libuv job: every non-detached child is assigned to one global job with `JOB_OBJECT_LIMIT_BREAKAWAY_OK | JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK | JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION | JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. The comment states the intent: "only the processes that we explicitly add are affected, and *their* subprocesses are not ... It also lets our child processes created detached processes without explicitly breaking away from job control" (`uv__init_global_job_handle`, `src/win/process.c`, link above).
- Python SDK: `src/mcp/client/stdio.py` (`_stop_server_process`, `_create_platform_compatible_process`), `src/mcp/os/posix/utilities.py` (`terminate_posix_process_tree`), `src/mcp/os/win32/utilities.py` (`_create_job_object`, `close_process_job`, `terminate_windows_process_tree`), all at <https://github.com/modelcontextprotocol/python-sdk/tree/77001c96f9507be0f4d009b83210a384a5e08bda/src/mcp>. Two statements from that source matter here. POSIX: "killpg reaches every descendant atomically, even ones whose parent already exited; daemonizers that left the group escape by design." Windows: "KILL_ON_JOB_CLOSE makes the close also kill any members still alive, deterministically rather than at GC time; a deliberate divergence from POSIX, where a graceful server's children are left alive."
- Go SDK: `mcp/cmd.go`, `pipeRWC.Close`, <https://github.com/modelcontextprotocol/go-sdk/blob/0fa5e8407aa3fda65166e4207156c03d084bfd44/mcp/cmd.go>.
- VS Code: `src/vs/workbench/api/node/extHostMcpNode.ts` (spawn), `src/vs/workbench/contrib/mcp/node/mcpStdioStateHandler.ts` (state machine, `GRACE_TIME_MS = 10_000`), `src/vs/base/node/processes.ts` (`killTree`), `src/vs/base/node/terminateProcess.sh` (`pgrep -P` recursion), all at <https://github.com/microsoft/vscode/tree/7588e115f8655dcf9784335fa5d86653a75673dd>.
- Codex: `codex-rs/rmcp-client/src/stdio_server_launcher.rs` (`ProcessMode::NewGroup`, `JobObject::create_without_breakaway()`, `PROCESS_GROUP_TERM_GRACE_PERIOD = 2 s`, `terminate()` called from `close()` and from `Drop`), `codex-rs/utils/pty/src/win/job.rs` (`create_without_breakaway` sets only `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`), `codex-rs/utils/pty/src/process_group.rs`, all at <https://github.com/openai/codex/tree/69231e119bfca0b14453515e97d99688ce1c47c9>.
- Claude Code: <https://code.claude.com/docs/en/mcp>, section on stopping a stdio server: "On macOS and Linux: Claude Code sends `SIGINT`, then `SIGTERM` if the process is still running 100 milliseconds later, then `SIGKILL` after a further 400 milliseconds. On Windows: Claude Code closes the server's stdin, then ends the server's process tree if the process is still running 300 milliseconds later. Before v2.1.296, Claude Code on Windows ended the server's process tree at once, without closing stdin first or waiting."

### Hosts that could not be verified

- **Claude Desktop**: closed source, no public documentation of stdio shutdown found. **Unverified.** If it uses the TypeScript SDK transport unmodified, the TypeScript row applies; that is an assumption, not a finding.
- **Cursor**: closed source, no official documentation found. Forum threads report leaked MCP processes after window close, which is anecdotal and says nothing about what Cursor does when it does kill. **Unverified.**
- **Claude Code**: the sequence and timings are documented, the mechanism is not. Whether the POSIX signals go to a PID or a process group, and whether the Windows "process tree" is a parent-PID walk or a job object, is **unverified**.

### Which of these would also kill a detached shared server

"Detached" below means: on POSIX, `Setsid` with nil stdio; on Windows, `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP` with nil stdio.

| Mechanism | Used by | Kills a detached shared server? |
| --- | --- | --- |
| Close stdin | Spec, all SDKs, VS Code, Codex, Claude Code on Windows | No. The shared server does not hold the pipe. |
| Signal to the server PID | TypeScript SDK, Go SDK | No. |
| `killpg` on the server's process group | Python SDK, Codex on Unix | No. `Setsid` puts the shared server in another group. `Setpgid` alone would also suffice here. |
| Parent-PID tree walk (`pgrep -P`, `taskkill /T`) | VS Code after 10 s (POSIX polite, both forceful), possibly Claude Code on Windows after 300 ms | **Yes, while the session proxy is alive**, because the shared server is its direct child. Not reached if the proxy exits within the grace period. Avoided structurally by the launcher indirection on POSIX (**inference**); on Windows **unverified**. |
| libuv global job (Node hosts on Windows) | Any host that spawns through Node `child_process` | No. `SILENT_BREAKAWAY_OK` means the server's own children are never job members. |
| Kill-on-close job without breakaway | Python SDK on Windows, Codex on Windows | **Yes, always, including on a clean shutdown**, and `CREATE_BREAKAWAY_FROM_JOB` cannot be used. The Python SDK assigns the job after spawn, so a child created before assignment escapes, but that is a race, not a mechanism. Codex spawns suspended, so there is no race. |

Consequences for the design:

- On POSIX the session proxy must exit on stdin EOF and on `SIGINT`/`SIGTERM`, well inside 500 ms (Claude Code) and without waiting on the shared server. This keeps every host on the first step of its sequence.
- On POSIX the only verified mechanism that can reach a `Setsid` shared server is VS Code's tree walk against a hung proxy. The launcher indirection removes it.
- On Windows the outcome depends on which host starts the shared server. Started under a Node-based host, it survives. Started under Codex or a Python SDK host, it dies with that session, taking the other attached sessions' server with it. That directly violates the map's invariant that no session is interrupted because of another session.

## 3. Environments where a detached process is killed or disallowed anyway

- **systemd units and scopes (Linux).** `KillMode=` "Defaults to `control-group`": "all remaining processes in the control group of this unit will be killed on unit stop" (**verified**, <https://www.freedesktop.org/software/systemd/man/latest/systemd.kill.html>). `setsid` does not change cgroup membership (**inference**; cgroup membership is unrelated to sessions and process groups). A shared server started from a host running inside a service, a transient scope, or a terminal emulator's per-tab scope dies when that unit is stopped. A scope whose processes simply exit is not "stopped" in this sense; the kill applies to an explicit stop.
- **Logout with `KillUserProcesses=yes` (Linux).** "If true, the scope unit corresponding to the session and all processes inside that scope will be terminated. If false, the scope is 'abandoned' ... and processes are not killed." The upstream man page gives the default as "yes" and notes that this "will break tools like screen(1) and tmux(1), unless they are moved out of the session scope" (**verified**, <https://www.freedesktop.org/software/systemd/man/latest/logind.conf.html>). Which distributions override the default was not verified. Given the map's scope (one machine, one OS user), a shared server dying at logout is acceptable: its sessions die at the same time.
- **Escaping a cgroup** requires asking systemd for a new transient unit (`systemd-run --user --scope`, or the D-Bus equivalent). That is a dependency on systemd being present and reachable and is out of proportion for this feature (**inference**).
- **Containers without an init.** When PID 1 of a PID namespace exits, "the kernel terminates all of the processes in the namespace via a SIGKILL signal" (**verified**, <https://man7.org/linux/man-pages/man7/pid_namespaces.7.html>). If the host is PID 1, or the container is stopped, the shared server goes with it; this is consistent with every session being gone. The separate hazard is reaping: an orphaned shared server is re-parented to the container's PID 1, and if that process does not reap, the server lingers as a zombie after it exits. `docker run --init` provides "an init inside the container that forwards signals and reaps processes" (**verified**, <https://docs.docker.com/reference/cli/docker/container/run/>). The Python SDK source carries the same warning for clients running as PID 1.
- **Processes started through `docker exec` or a dev-container attach.** Lifetime of orphans after the exec session ends was not verified.
- **Windows jobs without breakaway.** Covered above. Besides Codex and the Python SDK, any launcher, CI runner or terminal that wraps the host in such a job has the same effect (**inference**).
- **Sandboxed hosts.** Whether any host runs MCP servers inside a sandbox that forbids `setsid`, forbids creating new processes, or tears down a sandbox-wide process set (for example a `bwrap --die-with-parent` style wrapper, macOS App Sandbox, Windows AppContainer or packaged-app runtime) is **unverified** for all five hosts. No evidence of it was found in the VS Code or Codex MCP launch paths read for section 2.
- **macOS.** No OS-level mechanism equivalent to cgroups or jobs was found in the sources read that would kill an orphaned `Setsid` process while the user stays logged in. Behaviour at logout and under App Sandbox was not researched to primary sources: **unverified**.

Design implication: detachment can fail or be undone outside the server's control. The session proxy needs a detection path (the shared server never becomes reachable, or disappears) that ends in the already-decided restart behaviour, and the spec should say what happens when restart keeps failing.

## 4. Windows: parity or fallback

**Recommendation: fall back to the per-session server on Windows.** Treat Windows parity as out of scope for the first spec.

Reasons, in order of weight:

1. **Two verified host families make detachment impossible by construction.** Codex and Python SDK hosts put the server in a kill-on-close job with no breakaway and terminate or close that job on every shutdown. Nothing a `CreateProcess` child can do changes that. A shared server started from such a session would be killed when that one session ends, interrupting every other attached session. Crash recovery would mask it as repeated restarts with failed in-flight requests, which is precisely what the invariant rules out.
2. **Where it does work, it works by an implementation detail.** Under Node-based hosts the shared server survives only because libuv happens to set `JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK`. That is not a contract of any host.
3. **Three of five named hosts are unverifiable on Windows.** Claude Code documents a "process tree" kill without a mechanism; Claude Desktop and Cursor document nothing.
4. **The escape hatches are not acceptable defaults.** WMI `Win32_Process.Create`, Task Scheduler, or parent-process spoofing all leave the job, and all look like evasion to endpoint security products.
5. **Cost.** Windows needs its own spawn path, a direct `golang.org/x/sys/windows` dependency, its own liveness and shutdown protocol (no signals), and multi-process integration tests on a Windows runner. CI for this repository currently runs on `ubuntu-latest` only (`.github/workflows/ci.yml`), so none of it would be exercised today.

A sound later option, if demand appears: make it opt-in on Windows and gate it at runtime. Before starting a shared server the session proxy checks `IsProcessInJob` and the job's limit flags; if it is in a job that permits neither breakaway mode it does not start a shared server (it may still attach to one that already exists outside any job, or run per-session). This keeps the invariant, at the cost of the separate code path and Windows CI. Job-flag inspection with nested jobs was not researched and would need its own check.

Tech debt this fallback creates and that the spec should state: the "default on" contract becomes platform-conditional, and Windows users get one index per session as today.

## Open items

- Claude Code: PID versus process-group signalling on POSIX; mechanism of the Windows process-tree kill.
- Claude Desktop and Cursor: shutdown behaviour on every platform.
- `taskkill /T` and VS Code's `pgrep -P` walk against a grandchild whose intermediate parent has exited: expected to miss it, not tested.
- Exact `CreateProcess` error when `CREATE_BREAKAWAY_FROM_JOB` is refused.
- Distribution defaults for `KillUserProcesses`.
- macOS logout and App Sandbox behaviour.
- All of section 1 and 2 is from reading source and documentation. A small prototype that starts a `Setsid` child under each host and observes it across session end would convert the POSIX inferences into facts.
