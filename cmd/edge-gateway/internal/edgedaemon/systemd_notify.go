package edgedaemon

import (
	"net"
	"os"
	"strings"
	"time"
)

const systemdNotifyTimeout = time.Second

func systemdNotifyReady() error {
	return systemdNotify("READY=1")
}

func systemdNotifyWatchdog() error {
	return systemdNotify("WATCHDOG=1")
}

func systemdNotifyStopping() error {
	return systemdNotify("STOPPING=1")
}

func systemdNotify(state string) error {
	return notifySystemdSocket(os.Getenv("NOTIFY_SOCKET"), state)
}

func notifySystemdSocket(socketPath, state string) error {
	addr := strings.TrimSpace(socketPath)
	msg := strings.TrimSpace(state)
	if addr == "" || msg == "" {
		return nil
	}
	if (!strings.HasPrefix(addr, "/") && !strings.HasPrefix(addr, "@")) || len(addr) < 2 {
		return nil
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{
		Name: addr,
		Net:  "unixgram",
	})
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetWriteDeadline(time.Now().Add(systemdNotifyTimeout))
	_, err = conn.Write([]byte(msg))
	return err
}
