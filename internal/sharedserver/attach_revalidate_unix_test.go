//go:build unix

package sharedserver

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dialAttachRevalidateSocket(t *testing.T, socketPath string, done <-chan error) net.Conn {
	t.Helper()
	deadline := time.Now().Add(serveTestDeadline)
	for {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			t.Cleanup(func() { _ = conn.Close() })
			return conn
		}
		select {
		case err := <-done:
			t.Fatalf("Run returned before accepting: %v", err)
		default:
		}
		require.True(t, time.Now().Before(deadline), "socket never accepted: %v", err)
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRun_EveryAttachRevalidatesBeforeTheReadyLine(t *testing.T) {
	dir := shortTempDir(t)
	build, _ := buildSpy(nil)
	revalidating := make(chan struct{})
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Identity:    testIdentity,
			EndpointDir: dir,
			Build:       build,
			Revalidate: func(context.Context) {
				revalidating <- struct{}{}
				<-release
			},
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(serveTestDeadline):
			t.Error("Run did not stop")
		}
	})
	socketPath := filepath.Join(dir, testIdentity+".sock")

	for attachment := range 2 {
		conn := dialAttachRevalidateSocket(t, socketPath, done)
		select {
		case <-revalidating:
		case <-time.After(serveTestDeadline):
			t.Fatalf("attach %d did not revalidate", attachment)
		}

		require.NoError(t, conn.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
		_, err := conn.Read(make([]byte, 1))
		require.ErrorIs(t, err, os.ErrDeadlineExceeded, "attach %d: the ready line must wait for the revalidation", attachment)

		release <- struct{}{}
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(serveTestDeadline)))
		line, err := bufio.NewReader(conn).ReadString('\n')
		require.NoError(t, err)
		assert.Contains(t, line, `"ready":true`, "attach %d", attachment)
	}
}

func TestRun_AttachWithoutARevalidateHookSendsTheReadyLine(t *testing.T) {
	dir := shortTempDir(t)
	build, _ := buildSpy(nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Config{Identity: testIdentity, EndpointDir: dir, Build: build}) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(serveTestDeadline):
			t.Error("Run did not stop")
		}
	})

	conn := dialAttachRevalidateSocket(t, filepath.Join(dir, testIdentity+".sock"), done)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(serveTestDeadline)))
	line, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	assert.Contains(t, line, `"ready":true`)
}
