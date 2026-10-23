// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"context"
	"errors"
	"hash/fnv"
	"time"

	"komarugram/internal/messenger/proxy"
)

// demoPing makes up how long a proxy takes, as the demo reaches nothing: the
// same for the same proxy, and a failure for some of them, to show what each
// one looks like.
func demoPing(ctx context.Context, p *proxy.Proxy) (time.Duration, error) {
	sum := fnv.New32a()
	if p == nil {
		sum.Write([]byte("direct"))
	} else {
		sum.Write([]byte(p.ID()))
	}
	h := sum.Sum32()
	select {
	case <-time.After(time.Duration(200+h%600) * time.Millisecond):
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if p == nil {
		return 38 * time.Millisecond, nil
	}
	switch h % 6 {
	case 0:
		return 0, context.DeadlineExceeded
	case 1:
		return 0, errors.New("socks connect tcp: username/password authentication failed")
	case 2:
		return 0, errors.New("dial tcp: connection refused")
	}
	return time.Duration(40+h%900) * time.Millisecond, nil
}
