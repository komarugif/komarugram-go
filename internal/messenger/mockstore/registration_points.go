// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"sync"
	"time"

	"komarugram/internal/messenger/model"
)

// registrationState is what the demo was asked about its points: made-up
// users, nothing sent anywhere.
type registrationState struct {
	sync.Mutex
	asked map[int64]bool
	sent  map[int64]bool
	never bool
}

// demoRegistrationPoints are the demo's made-up points, enough for the
// window to ask about them.
var demoRegistrationPoints = []model.RegistrationPoint{
	{User: 5146523441, Date: time.Date(2022, 3, 2, 0, 0, 0, 0, time.UTC)},
	{User: 5893301277, Date: time.Date(2022, 12, 20, 0, 0, 0, 0, time.UTC)},
	{User: 6410772958, Date: time.Date(2023, 9, 14, 0, 0, 0, 0, time.UTC)},
	{User: 7021938465, Date: time.Date(2024, 4, 11, 0, 0, 0, 0, time.UTC)},
	{User: 7459917310, Date: time.Date(2024, 10, 3, 0, 0, 0, 0, time.UTC)},
	{User: 7988120534, Date: time.Date(2025, 5, 27, 0, 0, 0, 0, time.UTC)},
	{User: 8301447729, Date: time.Date(2025, 9, 9, 0, 0, 0, 0, time.UTC)},
}

// RegistrationPoints implements model.RegistrationPointSource.
func (s *Store) RegistrationPoints(context.Context) ([]model.RegistrationPoint, int, bool, error) {
	r := &s.registration
	r.Lock()
	defer r.Unlock()
	var points []model.RegistrationPoint
	unasked := 0
	for _, p := range demoRegistrationPoints {
		if r.sent[p.User] {
			continue
		}
		points = append(points, p)
		if !r.asked[p.User] {
			unasked++
		}
	}
	return points, unasked, r.never, nil
}

// SendRegistrationPoints implements model.RegistrationPointSource: the demo
// sends nothing, and remembers the points as sent.
func (s *Store) SendRegistrationPoints(_ context.Context, points []model.RegistrationPoint) error {
	s.rememberRegistration(points, true, false)
	return nil
}

// PostponeRegistrationPoints implements model.RegistrationPointSource.
func (s *Store) PostponeRegistrationPoints(_ context.Context, points []model.RegistrationPoint, never bool) error {
	s.rememberRegistration(points, false, never)
	return nil
}

func (s *Store) rememberRegistration(points []model.RegistrationPoint, sent, never bool) {
	r := &s.registration
	r.Lock()
	defer r.Unlock()
	if r.asked == nil {
		r.asked, r.sent = map[int64]bool{}, map[int64]bool{}
	}
	for _, p := range points {
		r.asked[p.User] = true
		r.sent[p.User] = r.sent[p.User] || sent
	}
	r.never = r.never || never
}
