# Research: local IPC channel between session proxy and shared server

Ticket: [#142](https://github.com/sha1n/mcp-acdc-server/issues/142). Parent map: [#139](https://github.com/sha1n/mcp-acdc-server/issues/139).
Date: 2026-11-13. Toolchain inspected: Go 1.27.3 (`go.mod` declares `go 1.27.0`).

This is a throwaway research note. It lives on the branch `research/local-ipc-channel`, which is never merged.

## Conventions

- **[V]** verified: read directly in the cited primary source during this research.
- **[I]** inference: reasoned from verified facts, not stated by a source.
- **[U]** unverified: believed true, but no primary source was read or no test was run.

Go source references are to the Go 1.27.3 tree (`$GOROOT/src/...`), browsable at `https://github.com/golang/go/tree/go1.27.3/src/...`.

## Summary and recommendation

| OS | Recommended channel | Endpoint location | Same-user restriction | Startup mutex |
| --- | --- | --- | --- | --- |
| Linux | Unix domain socket (pathname, `SOCK_STREAM`) | `$XDG_RUNTIME_DIR/acdc-mcp/<id>.sock`; fallback `os.TempDir()/acdc-mcp-<uid>/<id>.sock` | `0700` directory owned by the user, verified before use; `SO_PEERCRED` uid check as defence in depth | Exclusive `flock` on `<id>.lock` beside the socket |
| macOS | Unix domain socket (pathname, `SOCK_STREAM`) | `os.UserCacheDir()/acdc-mcp/<id>.sock` (alternative: `$TMPDIR`) | Same, with `LOCAL_PEERCRED` | Same |
| Windows | Unix domain socket through the same `net` code path, gated on a runtime probe; otherwise fall back to the per-session server | `%LocalAppData%\acdc-mcp\<id>.sock` | Directory ACL inherited from the user profile | Lock file (`LockFileEx`) |

Loopback TCP is not recommended on any OS. The named pipe is the technically stronger Windows primitive (no stale endpoint, atomic first-instance creation, explicit DACL) but costs a second transport implementation and a new dependency; it is the documented upgrade path if `AF_UNIX` on Windows proves unreliable. All of the recommendation rows are **[I]**; the facts they rest on are below.

Key facts later decisions depend on:

1. `net.Listen("unix", ...)` and `net.Dial("unix", ...)` compile and work on Linux, macOS, and Windows from the standard library alone. **[V]**
2. The socket path budget is the hard constraint: 103 usable bytes on macOS, 107 on Linux and Windows. `<id>` must be a short fixed-length hash. **[V]**
3. A pathname socket is not removed when its owner dies. Binding to a leftover path fails with `EADDRINUSE`, so bind alone cannot be the startup mutex on Unix; a separate lock is required to make stale-endpoint recovery race-free. **[V]** for the facts, **[I]** for the conclusion.
4. Windows `AF_UNIX` needs Windows 10 build 17063 or later, while Go still supports Windows Server 2016 (build 14393). A runtime fallback is therefore mandatory on Windows. **[V]**
5. Loopback TCP has no OS-level per-user access control; every tool surveyed that uses it adds an application-level secret or accepts the exposure. **[V]** for the tools, **[I]** for the generalisation.

## 1. Availability and Go support per OS

### Unix domain sockets in Go

- `net/unixsock_posix.go` carries the build constraint `//go:build unix || js || wasip1 || windows`, so the same `UnixListener` / `UnixConn` implementation is compiled on Windows. **[V]** (`$GOROOT/src/net/unixsock_posix.go`, line 5)
- Windows support entered Go through issue [golang/go#26072](https://github.com/golang/go/issues/26072), "net: Support AF_UNIX on Windows10 or later", closed by a commit on 2018-08-28 (CL 125456, "syscall: implement Unix Socket for Windows"). **[V]** The release that first shipped it is Go 1.12. **[U]** (the issue page does not name the release)
- Go probes for Windows support at run time: `SupportUnixSocket` enumerates Winsock protocols and looks for `AF_UNIX`; its comment states "The minimal requirement is Windows 10.0.17063." **[V]** (`$GOROOT/src/internal/syscall/windows/version_windows.go`, lines 117-139). This function is internal; application code cannot call it and must probe by attempting to listen. **[I]**
- Go's minimum Windows requirement: "For Go 1.21 and later: Windows 10 and higher or Windows Server 2016 and higher." **[V]** ([go.dev/wiki/MinimumRequirements](https://go.dev/wiki/MinimumRequirements)). Windows Server 2016 is build 14393, which predates 17063, so a Go binary can run on a supported Windows that lacks `AF_UNIX`. **[I]**
- Microsoft's announcement: "Beginning in Insider Build 17063, you'll be able to use the unix socket (AF_UNIX) address family on Windows." Only `SOCK_STREAM` is supported. Unsupported: `SOCK_DGRAM`, `SOCK_SEQPACKET`, ancillary data ("There is no support for ancillary data in the Windows unix socket implementation", which rules out `SCM_RIGHTS` and `SCM_CREDENTIALS`), autobind, and `socketpair`. **[V]** ([AF_UNIX comes to Windows](https://devblogs.microsoft.com/commandline/af_unix-comes-to-windows/))
- Go's own test suite contains `t.Skip("unix sockets not reliable on windows")` in `TestUnixConnReadEOF`. **[V]** (`$GOROOT/src/net/unixsock_test.go`, lines 485-486). The scope of that unreliability is not documented there. **[U]**
- `os.Stat` on Windows reports `ModeSocket` for a socket file by recognising the reparse tag `IO_REPARSE_TAG_AF_UNIX`. **[V]** (`$GOROOT/src/os/types_windows.go`, lines 210-211, 257-258)
- macOS supports only `SOCK_STREAM` and `SOCK_DGRAM` for the Unix domain. **[V]** (macOS `unix(4)` man page, read locally on macOS 27.2). The channel needs only `SOCK_STREAM`. **[I]**

### Path-length limits

| OS | `sun_path` size | Longest name Go accepts | Source |
| --- | --- | --- | --- |
| Linux | 108 | 107 bytes (NUL terminator required) | `$GOROOT/src/syscall/ztypes_linux_amd64.go:172`; `syscall_linux.go:547-560` |
| macOS, FreeBSD | 104 | 103 bytes (`n >= len(Path)` is rejected) | `$GOROOT/src/syscall/ztypes_darwin_arm64.go:176`; `syscall_bsd.go:188-193` |
| Windows | 108 (`UNIX_PATH_MAX`, "defined in afunix.h") | 107 bytes | `$GOROOT/src/syscall/types_windows.go:1212`; `syscall_windows.go:902-916` |

All **[V]**. Additional sources:

- Linux `unix(7)`: "On Linux, sun_path is 108 bytes in size" and "some implementations have sun_path as short as 92 bytes." **[V]** ([unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html))
- macOS `unix(4)`: "UNIX-domain addresses are variable-length filesystem pathnames of at most 104 characters." **[V]** Confirmed in the SDK header `sys/un.h` (`char sun_path[104]`). **[V]**
- Go returns `EINVAL` from the sockaddr conversion when the name is too long, before any system call; the user-visible error is "invalid argument" rather than a length-specific message. **[V]** for the `EINVAL`, **[I]** for the user-visible consequence.

Budget examples **[I]**, assuming a 16-hex-character identity and the layout `<dir>/acdc-mcp/<id>.sock` (30 bytes after `<dir>`):

- Linux `$XDG_RUNTIME_DIR` = `/run/user/1000`: 44 bytes. Comfortable.
- macOS `$TMPDIR`: the value on the research machine is 49 bytes (`/var/folders/xx/<30 chars>/T/`), giving about 78 bytes. Fits.
- macOS `~/Library/Caches`: `/Users/<name>/Library/Caches` is 23 bytes plus the user name, giving 53 plus the user name. Fits unless the user name exceeds 50 bytes.
- Windows `%LocalAppData%`: `C:\Users\<name>\AppData\Local` is 23 bytes plus the user name. Similar to macOS. Roaming or redirected profiles can be longer. **[U]**

The implementation must check the final path length and treat an over-long path as "sharing unavailable" rather than fail. A relocated `HOME`, `XDG_RUNTIME_DIR`, or `TMPDIR` can exceed the limit on any OS. **[I]**

### Named pipes in Go

- The standard library has no public named-pipe listener or dialer. `CreateNamedPipe` exists only in `internal/syscall/windows`. **[V]** (`$GOROOT/src/internal/syscall/windows/syscall_windows.go:532`)
- The de facto implementation is [`github.com/microsoft/go-winio`](https://github.com/microsoft/go-winio/blob/main/pipe.go) (`ListenPipe`, `DialPipe`), which returns `net.Listener` / `net.Conn`. **[V]** It is not currently in this repository's `go.mod`. **[V]**
- Pipe names have the form `\\.\pipe\<pipename>`; "The entire pipe name string can be up to 256 characters long. Pipe names are not case sensitive." **[V]** ([CreateNamedPipeA](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createnamedpipea))

### Loopback TCP in Go

Available on every OS through `net.Listen("tcp", "127.0.0.1:0")`. **[V]** (general `net` behaviour). It needs a side channel (a file) to publish the chosen port, or a fixed port, which cannot be unique per server identity. **[I]**

## 2. Restricting access to the same OS user

### Unix domain socket on Linux

- "In the Linux implementation, pathname sockets honor the permissions of the directory they are in." and "On Linux, connecting to a stream socket object requires write permission on that socket". **[V]** ([unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html))
- Portability caveat from the same page: "on some systems (e.g., older BSDs), the socket permissions are ignored" and "Portable programs should not rely on this feature for security." **[V]** The portable control is therefore the mode of the containing directory, not the mode of the socket file. **[I]**
- `SO_PEERCRED`: "This read-only socket option returns the credentials of the peer process connected to this socket." **[V]** Go exposes it as `syscall.GetsockoptUcred` and `golang.org/x/sys/unix.GetsockoptUcred`. **[V]** (`$GOROOT/src/syscall/syscall_linux.go:759`; `golang.org/x/sys@v0.49.0/unix/syscall_linux.go:1283`)
- Abstract sockets: "Socket permissions have no meaning for abstract sockets." **[V]** Any local user in the same network namespace can connect, so an abstract socket would require a mandatory `SO_PEERCRED` check. **[I]**

### Unix domain socket on macOS

- "Normal filesystem access-control mechanisms are also applied when referencing pathnames; e.g., the destination of a connect(2) or sendto(2) must be writable." **[V]** (macOS `unix(4)`)
- `LOCAL_PEERCRED` returns the peer's effective credentials as a `struct xucred`. "This mechanism is reliable; there is no way for either party to influence the credentials presented to its peer except by calling the appropriate system call (e.g., connect(2) or listen(2)) under different effective credentials." **[V]** (macOS `unix(4)`). Both the server and the client can use it, so a session proxy can also verify that the shared server belongs to the same user. **[V]**
- Go access: `golang.org/x/sys/unix.GetsockoptXucred(fd, SOL_LOCAL, LOCAL_PEERCRED)`. **[V]** (`golang.org/x/sys@v0.49.0/unix/syscall_darwin.go:487-489`). The standard `syscall` package has no equivalent on Darwin. **[V]** (searched `$GOROOT/src/syscall/syscall_darwin.go`). `golang.org/x/sys` is already an indirect dependency of this module. **[V]** (`go.mod`)

### Unix domain socket on Windows

- "Communication over unix sockets can be secured by controlling the file (or directory) permissions." **[V]** ([AF_UNIX comes to Windows](https://devblogs.microsoft.com/commandline/af_unix-comes-to-windows/))
- There is no credential passing (no ancillary data, so no `SCM_CREDENTIALS`). **[V]** A peer-PID ioctl (`SIO_AF_UNIX_GETPEERPID`) is reported to exist, but the announcement does not mention it and Go does not wrap it. **[U]**
- Go ignores Unix permission bits on Windows except the owner-writable bit: "On Windows, only the 0o200 bit (owner writable) of mode is used". **[V]** (`os.Chmod` documentation, `$GOROOT/src/os/file.go:636`). `os.MkdirAll(dir, 0o700)` therefore does not create a private directory on Windows; protection comes from the ACL inherited from the parent. **[I]**
- `%LocalAppData%` sits inside the user profile, whose default ACL grants access to the user, Administrators, and SYSTEM only. **[U]** (standard Windows behaviour; no primary source was read)

### Windows named pipe

- Default security when no descriptor is supplied: "The ACLs in the default security descriptor for a named pipe grant full control to the LocalSystem account, administrators, and the creator owner. They also grant read access to members of the Everyone group and the anonymous account." **[V]** ([Named Pipe Security and Access Rights](https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights)). The default is therefore not same-user-only; an explicit DACL is required. **[I]**
- "To prevent remote users or users on a different terminal services session from accessing a named pipe, use the logon SID on the DACL for the pipe." **[V]** (same page)
- `PIPE_REJECT_REMOTE_CLIENTS`: "Connections from remote clients are automatically rejected." **[V]** ([CreateNamedPipeA](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createnamedpipea)). go-winio sets `FILE_PIPE_REJECT_REMOTE_CLIENTS` on every instance. **[V]** ([go-winio `pipe.go`](https://github.com/microsoft/go-winio/blob/main/pipe.go))
- go-winio accepts the DACL as an SDDL string in `PipeConfig.SecurityDescriptor`. **[V]** Docker uses `D:P(A;;GA;;;BA)(A;;GA;;;SY)` plus one `(A;;GRGW;;;<SID>)` entry per extra principal. **[V]** ([moby `listeners_windows.go`](https://github.com/moby/moby/blob/master/daemon/listeners/listeners_windows.go)). A same-user pipe would use the current user's SID in place of those principals. **[I]**
- The server can identify the client process with `GetNamedPipeClientProcessId` ("Retrieves the client process identifier for the specified named pipe"). **[V]** ([docs](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-getnamedpipeclientprocessid))
- go-winio dials with `SECURITY_SQOS_PRESENT` and anonymous impersonation level by default, which stops a rogue pipe server from impersonating the client. **[V]** ([go-winio `pipe.go`](https://github.com/microsoft/go-winio/blob/main/pipe.go))

### Loopback TCP

- A TCP listener on `127.0.0.1` has no filesystem object and therefore no mode, owner, or ACL; `SO_PEERCRED` and `LOCAL_PEERCRED` are Unix-domain options. Any local process of any user can connect. **[I]** (follows from `unix(7)` scoping these options to `AF_UNIX`)
- Docker's documentation on its own TCP option: "When using a TCP socket, the Docker daemon provides un-encrypted and un-authenticated direct access" and "If you are binding to a TCP port, anyone with access to that port has full Docker access". **[V]** ([dockerd reference](https://docs.docker.com/reference/cli/dockerd/))
- What loopback TCP needs instead **[I]**, as demonstrated by Bazel **[V]**: a random secret stored in a file only the user can read, presented by the client on every request, plus a second secret returned by the server so the client can detect an impostor that grabbed the port. Bazel's client reads `server_info.rawproto` from the server directory, accepts only `127.0.0.1:`, `[::1]:`, or `[0:0:0:0:0:0:0:1]:` addresses, sends `request_cookie`, and rejects responses whose cookie differs from `response_cookie`. ([bazel `blaze.cc`](https://github.com/bazelbuild/bazel/blob/master/src/main/cpp/blaze.cc))
- The cost relative to a Unix socket: a port file, a secret file, an authentication handshake in the protocol, and exposure to any local user who can read the secret through a mistake in file permissions. **[I]**

## 3. Endpoint location per OS and reboot behaviour

### `XDG_RUNTIME_DIR` (Linux)

From the XDG Base Directory Specification, version 0.8 ([spec](https://specifications.freedesktop.org/basedir/latest/)), all **[V]**:

- "The directory MUST be owned by the user, and they MUST be the only one having read and write access to it. Its Unix access mode MUST be 0700."
- "The lifetime of the directory MUST be bound to the user being logged in. It MUST be created when the user first logs in and if the user fully logs out the directory MUST be removed."
- "Files in the directory MUST not survive reboot or a full logout/login cycle."
- "The directory MUST be on a local file system and not shared with any other system."
- It is intended for "communication and synchronization purposes" and may hold sockets and named pipes.
- "If `$XDG_RUNTIME_DIR` is not set applications should fall back to a replacement directory with similar capabilities".
- Files "MAY be subjected to periodic clean-up"; to prevent removal "they should have their access time timestamp modified at least once every 6 hours of monotonic time or the 'sticky' bit should be set on the file."

systemd implementation ([pam_systemd](https://www.freedesktop.org/software/systemd/man/latest/pam_systemd.html)), all **[V]**:

- The directory is `/run/user/$UID`, "either created or mounted as new `tmpfs` file system with quota applied".
- When "the last concurrent session of a user ends, the user runtime directory `/run/user/$UID` and all its contents are removed, too."
- "`$XDG_RUNTIME_DIR` is not set if the current user is not the original user of the session." It is also absent when systemd is not the init system (the module "does nothing").

Consequences **[I]**:

- Reboot or full logout removes the socket, so no stale endpoint survives a reboot on a systemd desktop.
- A shared server that outlives the user's last login session loses its socket file while still running. The fixed invariant (the shared server exits once all sessions disconnect) makes that window small, since session proxies are children of MCP hosts in the same login session.
- The variable is frequently unset in containers, `su` shells, cron, and minimal distributions. A fallback is mandatory.
- Go has no `os.UserRuntimeDir`. **[V]** (`$GOROOT/src/os/file.go` defines only `UserCacheDir`, `UserConfigDir`, `UserHomeDir`, `TempDir`). The variable must be read directly.

### `os.TempDir`

- "On Unix systems, it returns $TMPDIR if non-empty, else /tmp. On Windows, it uses GetTempPath, returning the first non-empty value from %TMP%, %TEMP%, %USERPROFILE%, or the Windows directory." and "The directory is neither guaranteed to exist nor have accessible permissions." **[V]** (`$GOROOT/src/os/file.go:478-486`)
- Linux `/tmp` is world-writable with the sticky bit (`1777`), and systemd's default policy deletes entries older than 10 days (`q /tmp 1777 root root 10d`). **[V]** ([systemd `tmp.conf`](https://github.com/systemd/systemd/blob/main/tmpfiles.d/tmp.conf)). Whether `/tmp` is cleared at reboot depends on the distribution (tmpfs or not). **[U]**
- A socket under `/tmp` is safe only inside a subdirectory that the process creates with mode `0700` and then verifies (owner equals the current uid, is a directory, not a symlink, no group or other bits), because another user can pre-create the predictable name. **[I]** ssh-agent's `-T` mode uses a randomised subdirectory `$TMPDIR/ssh-XXXXXXXXXX/agent.<ppid>` for the same reason, but a random name cannot serve as a rendezvous point, so verification replaces randomness here. **[V]** for ssh-agent ([ssh-agent(1)](https://man.openbsd.org/ssh-agent.1)), **[I]** for the rest.
- macOS sets `TMPDIR` to the per-user directory also reported by `confstr(_CS_DARWIN_USER_TEMP_DIR)`: "This directory is created with access permissions of 0700 ... By default, files in this location may be cleaned (removed) by the system if they are not accessed in 3 days." **[V]** (macOS `confstr(3)` man page, read locally). That `TMPDIR` equals this directory was observed on the research machine. **[V]** Whether connecting to a socket counts as an access for that cleaner, and whether the directory is emptied at reboot, is not documented in the pages read. **[U]**
- Windows: for non-SYSTEM processes the order is `TMP`, `TEMP`, `USERPROFILE`, then the Windows directory; "the function does not verify that the path exists". **[V]** ([GetTempPath2W](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-gettemppath2w)). Go prefers `GetTempPath2` when the API is present. **[V]** (`$GOROOT/src/os/file_windows.go:284-308`)

### `os.UserCacheDir`

- Linux and other Unix: `$XDG_CACHE_HOME`, else `$HOME/.cache`; error if neither is defined or the XDG value is relative. macOS: `$HOME/Library/Caches`. Windows: `%LocalAppData%`. **[V]** (`$GOROOT/src/os/file.go`, `UserCacheDir`)
- These directories persist across reboots, so a socket file left by a crash or a power loss is still present after reboot. Stale-endpoint handling is then exercised routinely rather than rarely. **[I]**
- The XDG specification defines the cache directory for "user-specific non-essential data files", and a home directory may be on a network filesystem, where Unix sockets and `flock` are unreliable. The runtime directory, by contrast, "MUST be on a local file system". **[V]** for the quotes; `flock(2)` notes that NFS emulates `flock` with byte-range locks ([flock(2)](https://man7.org/linux/man-pages/man2/flock.2.html)). **[V]** This is why the cache directory is the wrong default on Linux. **[I]**

### Per-OS conclusion **[I]**

- **Linux:** `$XDG_RUNTIME_DIR/acdc-mcp/` when the variable is set and the directory passes the ownership check; otherwise `os.TempDir()/acdc-mcp-<uid>/` created `0700` and verified. This matches gopls, which uses `$XDG_RUNTIME_DIR` and falls back to `os.TempDir()`. **[V]** for gopls ([`autostart_posix.go`](https://github.com/golang/tools/blob/master/gopls/internal/lsprpc/autostart_posix.go))
- **macOS:** `os.UserCacheDir()/acdc-mcp/` created `0700`. It is on the local boot volume for ordinary accounts, is not subject to the 3-day temp cleaner, and leaves about 50 bytes for the user name. `$TMPDIR` is the alternative: already `0700` and per-user, but exposed to the cleaner for a long-lived shared server. The choice between the two is a spec decision; both fit the path budget.
- **Windows:** `%LocalAppData%\acdc-mcp\`. Not the temp directory: `%TMP%` is routinely purged by cleanup tools. **[U]**

## 4. Stale endpoints and bind as the mutual-exclusion primitive

### Verified behaviour

- A bound pathname socket outlives its process. Linux: the socket "must be deleted by the caller when it is no longer needed (using unlink(2))". macOS: "This file is not removed when the socket is closed—unlink(2) must be used to remove the file." **[V]** ([unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html); macOS `unix(4)`)
- Binding to a path that already exists fails with `EADDRINUSE`: "The specified local address is already in use or the filesystem socket object already exists." This holds whether or not anything is listening. **[V]** ([unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html))
- Connecting to a socket file with no listener fails with `ECONNREFUSED`: "The remote address specified by connect(2) was not a listening socket." macOS: "The attempt to connect was ignored (because the target is not listening for connections) or explicitly rejected." **[V]** ([unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html); macOS `connect(2)`)
- Go removes the socket file on a clean `UnixListener.Close` when the listener created it (`unlink: true` in `listenUnix`; `SetUnlinkOnClose` controls it). A killed or crashed process never runs `Close`, so the file stays. **[V]** (`$GOROOT/src/net/unixsock_posix.go:180-230`)
- The Go source acknowledges the removal race: "There's a race here--we can't know for sure whether someone else has come along and replaced our socket name already". **[V]** (same file, lines 183-189). A shared server that closes its listener after a successor has rebound the same path deletes the successor's socket file. **[I]**
- On Windows the socket file must also be deleted before rebinding: "DeleteFile (or any other file delete API) should be used to delete the socket file prior to calling bind with the same path." **[V]** ([AF_UNIX comes to Windows](https://devblogs.microsoft.com/commandline/af_unix-comes-to-windows/)). The Windows error codes for the stale and missing cases (expected `WSAECONNREFUSED`, `WSAEADDRINUSE`) were not tested. **[U]**
- Linux abstract sockets "automatically disappear when all open references to the socket are closed." **[V]** ([unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html))
- Named pipe: with `FILE_FLAG_FIRST_PIPE_INSTANCE`, "creation of the first instance succeeds, but creation of the next instance fails with ERROR_ACCESS_DENIED", and "An instance of a named pipe is always deleted when the last handle to the instance of the named pipe is closed." **[V]** ([CreateNamedPipeA](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createnamedpipea)). go-winio's `ListenPipe` creates the first instance with the `FILE_CREATE` disposition, the native equivalent. **[V]** ([go-winio `pipe.go`](https://github.com/microsoft/go-winio/blob/main/pipe.go))
- `flock`: the lock is released "when all such file descriptors have been closed", which includes process death. **[V]** ([flock(2)](https://man7.org/linux/man-pages/man2/flock.2.html)). Go exposes `syscall.Flock` on Unix; the standard library's portable wrapper is internal to `cmd/go` (`cmd/go/internal/lockedfile`), so application code needs `syscall.Flock` plus `LockFileEx` through `golang.org/x/sys/windows`, or a library. **[V]** `github.com/gofrs/flock` is already an indirect dependency of this module. **[V]** (`go.mod`)

### Detecting a stale endpoint **[I]**

Dial the socket:

- Success: a shared server is listening. Proceed to the application handshake (version and server identity check).
- `ENOENT`: no endpoint. A shared server must be started.
- `ECONNREFUSED`: the file exists and nothing listens. The endpoint is stale, or a new shared server is between `bind` and `listen`. Go's `net.Listen` performs both calls back to back, which makes that window very small but not zero.

A dial result is only a snapshot. Acting on "stale" by unlinking is where the race lives.

### Can bind itself be the startup mutex?

| Primitive | Atomic "exactly one wins" | Released when the owner dies | Usable as the sole mutex |
| --- | --- | --- | --- |
| Unix pathname socket `bind` | Yes: the loser gets `EADDRINUSE` **[V]** | No: the file remains **[V]** | No **[I]** |
| Linux abstract socket `bind` | Yes **[V]** | Yes **[V]** | Yes, on Linux only, with no filesystem permissions **[I]** |
| Windows named pipe, first instance | Yes **[V]** | Yes **[V]** | Yes **[I]** |
| Loopback TCP `bind` on a fixed port | Yes | Yes | Only with a fixed port, which cannot be unique per server identity **[I]** |
| `flock` / `LockFileEx` on a lock file | Yes | Yes **[V]** | Yes, on a local filesystem **[I]** |

For the recommended pathname socket, bind is atomic among starters only while no stale file exists. Recovery requires unlink followed by bind, and that sequence is not atomic. The failure: starters A and B both observe `ECONNREFUSED`; A unlinks and binds; B then unlinks A's live socket and binds its own. Two shared servers now run for one server identity and A is unreachable. **[I]** gopls has exactly this sequence (stat, `os.Remove`, start the daemon) and marks it: "TODO(rfindley): there is probably a race here if multiple server instances are simultaneously starting up." **[V]** ([gopls `dialer.go`](https://github.com/golang/tools/blob/master/gopls/internal/lsprpc/dialer.go))

Recommended shape **[I]**: the shared server takes an exclusive, non-blocking lock on `<id>.lock` in the endpoint directory and holds it for its whole lifetime. Only the lock holder may unlink a leftover socket and bind. A starter that fails to take the lock knows a shared server is alive or starting, and retries the dial with backoff. The lock disappears with the process, so no stale-lock handling is needed, and the same design works on Windows through `LockFileEx`. Bazel uses the same separation: an exclusive lock on the output base serialises startup, and the client retries the connection until a startup timeout. **[V]** ([bazel `blaze.cc`](https://github.com/bazelbuild/bazel/blob/master/src/main/cpp/blaze.cc))

Two details follow **[I]**:

- The shared server should disable Go's unlink-on-close (`SetUnlinkOnClose(false)`) or unlink only while still holding the lock, so that a slow shutdown cannot delete a successor's socket.
- The session proxy should treat "dial succeeded but the handshake reports a different version or identity" as a distinct outcome from "stale", since the map fixes that mismatched versions do not share.

## 5. How well-known tools solve the same problem

| Tool | Channel | Location | Access control | Startup and stale handling |
| --- | --- | --- | --- | --- |
| gopls daemon (`-remote=auto`) | Unix socket on POSIX; `tcp` `localhost:37374` elsewhere | `$XDG_RUNTIME_DIR`, else `os.TempDir()`; file `<basename>-<6 hex of build ID hash>-daemon.<$USER>` | Checks that the socket file's owner uid equals the current uid, fails closed on mismatch; described in source as "imperfect". On Windows the check always returns true | One dial; on failure removes the socket file and starts the daemon; 8 retries with exponential backoff from 50 ms (about 12.8 s). Acknowledged startup race. Daemon exits after an idle timeout (default 1m) |
| Bazel server | gRPC over loopback TCP | Output base, derived from workspace path and user id | Request and response cookies stored in the server directory; client accepts only loopback addresses; verifies the server PID | Exclusive lock on the output base; kills and restarts a server that is unresponsive or of the wrong version; idle shutdown after 3 hours by default |
| ssh-agent | Unix socket; named pipe in the Windows port | `$HOME/.ssh/agent/` by default, or `$TMPDIR/ssh-XXXXXXXXXX/agent.<ppid>` with `-T` | "It is accessible only to the current user, but is easily abused by root or another instance of the same user." | Not auto-started; the path is passed through `SSH_AUTH_SOCK`. "The sockets should get automatically removed when the agent exits." |
| Docker daemon | Unix socket on Linux; named pipe on Windows | `/var/run/docker.sock` | `root` or `docker` group on the socket; SDDL DACL on the pipe | System service; not started on demand |

Sources, all **[V]** unless marked:

- gopls: [`autostart_posix.go`](https://github.com/golang/tools/blob/master/gopls/internal/lsprpc/autostart_posix.go), [`autostart_default.go`](https://github.com/golang/tools/blob/master/gopls/internal/lsprpc/autostart_default.go), [`dialer.go`](https://github.com/golang/tools/blob/master/gopls/internal/lsprpc/dialer.go), [`lsprpc.go`](https://github.com/golang/tools/blob/master/gopls/internal/lsprpc/lsprpc.go), [`daemon.md`](https://github.com/golang/tools/blob/master/gopls/doc/daemon.md). The gopls handshake compares executable paths, not versions, and only logs a mismatch. The socket name embeds a hash of the binary's build ID so that a rebuilt binary gets a separate daemon, which is the same effect the map requires for differing binary versions.
- Bazel: [Client/server implementation](https://bazel.build/run/client-server) and [`blaze.cc`](https://github.com/bazelbuild/bazel/blob/master/src/main/cpp/blaze.cc).
- ssh-agent: [ssh-agent(1)](https://man.openbsd.org/ssh-agent.1). The Windows port's pipe name `\\.\pipe\openssh-ssh-agent` is **[U]**; the Microsoft page read does not mention it.
- Docker: [dockerd reference](https://docs.docker.com/reference/cli/dockerd/), [Protect the Docker daemon socket](https://docs.docker.com/engine/security/protect-access/) ("By default, Docker runs through a non-networked UNIX socket."), [moby `listeners_windows.go`](https://github.com/moby/moby/blob/master/daemon/listeners/listeners_windows.go).

Pattern across the tools **[I]**: every tool that controls its own platform choice uses a Unix socket on Unix. On Windows the mature tools (Docker, OpenSSH) use named pipes; the one that reuses its Unix code with minimal effort (gopls) drops to unauthenticated loopback TCP on a fixed port and loses per-identity daemons there.

## 6. Options compared

| Criterion | Unix domain socket | Windows named pipe | Loopback TCP |
| --- | --- | --- | --- |
| Standard library support | Linux, macOS, Windows | None; needs `go-winio` or hand-written `x/sys/windows` code | All |
| Same-user restriction | Directory mode `0700`, plus peer credentials on Linux and macOS | Explicit DACL required; the default grants Everyone read | None from the OS; needs secret files and a handshake |
| Endpoint naming | Filesystem path, 103 to 107 bytes | `\\.\pipe\<name>`, 256 characters, no filesystem location to choose | Port number published through a file |
| Stale endpoint after a crash | Yes; needs lock plus unlink | No; the kernel removes it | No for the port; yes for the port and secret files |
| Bind as the mutex | No | Yes | No with a dynamic port |
| Extra code paths | One for all three OSes | A second transport on Windows | One, plus authentication |

## 7. Open points for the spec

1. Windows: accept `AF_UNIX` with a runtime probe and fallback (one code path, no new dependency, no peer credentials, reliability caveat in Go's own tests), or adopt named pipes through `go-winio` (stronger semantics, new direct dependency, second transport). The map already accepts falling back to the per-session server where parity is too costly.
2. macOS location: `os.UserCacheDir` or `$TMPDIR`. Depends on the unverified behaviour of the 3-day temp cleaner toward sockets.
3. Whether the peer-credential check is mandatory or defence in depth. With a verified `0700` directory it is redundant on Linux and macOS; it is the only control if the directory check is ever skipped.
4. Length of `<id>` in the socket name and the behaviour when the final path exceeds the limit (recommended: degrade to the per-session server).
5. File-lock implementation: `syscall.Flock` plus `x/sys/windows.LockFileEx` directly, or promote `github.com/gofrs/flock` from indirect to direct dependency.

## 8. Not verified

- No code was run on Windows or Linux; all Windows and Linux behaviour comes from documentation and Go source. Go and macOS facts were read on a macOS 27.2 machine with Go 1.27.3.
- The Go release that first shipped Windows `AF_UNIX` support (believed 1.12).
- The meaning and scope of "unix sockets not reliable on windows" in Go's test suite.
- Windows `AF_UNIX` error codes for a stale socket file and for a missing one, and whether `SIO_AF_UNIX_GETPEERPID` is available and documented.
- The default ACL of `%LocalAppData%` and the practical purge behaviour of `%TMP%`.
- Whether macOS empties `$TMPDIR` at reboot and whether its 3-day cleaner removes a socket that is only connected to.
- Whether `/tmp` survives reboot on a given Linux distribution.
- The OpenSSH for Windows agent pipe name.
- Bazel's `GrpcServerImpl.java` (server side of the cookie scheme) could not be fetched; only the client side in `blaze.cc` was read.
