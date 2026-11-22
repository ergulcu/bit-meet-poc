// Copyright 2022 Mehmet ERGULCU
// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"testing"

	"github.com/ergulcu/bit-meet-poc/internal/meeting"
	"github.com/ergulcu/bit-meet-poc/internal/store/storetest"
)

func TestStore(t *testing.T) {
	storetest.Run(t, func(*testing.T) meeting.Store { return New() })
}
