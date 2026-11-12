// This file redacts credentials from URLs before they reach logs.
package gateway

import "net/url"

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = nil
	return u.String()
}
