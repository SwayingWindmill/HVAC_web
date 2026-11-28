package workorderservice

import (
	"time"

	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

func newUUIDv7(now time.Time) (string, error) {
	return uuidv7.NewAt(now)
}
