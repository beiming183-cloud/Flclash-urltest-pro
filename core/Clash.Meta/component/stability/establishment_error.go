package stability

import "errors"

var ErrServiceStopped = errors.New("stability service stopped")

var ErrEstablishmentExhausted = errors.New("residential establishment attempt budget exhausted")
