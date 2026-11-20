package outboundgroup

import (
	"errors"
	"net/netip"
	"strings"
)

func verifyStableExit(expected string, fetch func(string) ([]byte, error)) (error, bool) {
	return verifyStableExitServices(expected, fetch, []string{"https://api.ipify.org", "https://www.cloudflare.com/cdn-cgi/trace"})
}

func verifyStableExitServices(expected string, fetch func(string) ([]byte, error), sites []string) (error, bool) {
	want, err := netip.ParseAddr(expected)
	if err != nil {
		return errors.New("unexpected_exit"), true
	}
	var last error
	for _, url := range sites {
		data, err := fetch(url)
		if err != nil {
			last = err
			_, hard := stableError(err)
			if hard {
				return err, true
			}
			continue
		}
		text := strings.TrimSpace(string(data))
		if url == "https://www.cloudflare.com/cdn-cgi/trace" {
			text = ""
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "ip=") {
					text = strings.TrimSpace(strings.TrimPrefix(line, "ip="))
					break
				}
			}
		}
		ip, err := netip.ParseAddr(text)
		if err != nil {
			last = errors.New("service_failed")
			continue
		}
		if ip.Unmap() != want.Unmap() {
			return errors.New("unexpected_exit"), true
		}
		return nil, false
	}
	return last, false
}
