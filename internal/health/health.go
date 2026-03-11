package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/jackc/verna/internal/ssh"
)

// WaitForHealthy polls the health endpoint via SSH until it returns 200 or the timeout expires.
func WaitForHealthy(client *ssh.Client, port int, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	interval := 500 * time.Millisecond
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
				return client.Dial("tcp", addr)
			},
		},
		Timeout: 2 * time.Second,
	}

	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(interval)
	}

	return fmt.Errorf("health check at %s did not pass within %s: %v", url, timeout, lastErr)
}
