package health

import (
	"fmt"
	"time"

	"github.com/jackc/verna/internal/ssh"
)

// WaitForHealthy polls the health endpoint via SSH until it returns 200 or the timeout expires.
func WaitForHealthy(client *ssh.Client, port int, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	interval := 500 * time.Millisecond
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	cmd := fmt.Sprintf("curl -sf %s", url)

	var lastErr error
	for time.Now().Before(deadline) {
		if _, err := client.Run(cmd); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(interval)
	}

	return fmt.Errorf("health check at %s did not pass within %s: %v", url, timeout, lastErr)
}
