package util

import (
	"github.com/mechta-market/pulse_bot/internal/domain/common/model"
	"github.com/mechta-market/pulse_bot/internal/errs"
)

const (
	defaultMaxPageSize int64 = 100
)

func RequirePageSize(pars model.ListParams, maxPageSize int64) error {
	if maxPageSize == 0 {
		maxPageSize = defaultMaxPageSize
	}

	if pars.PageSize == 0 || pars.PageSize > maxPageSize {
		return errs.IncorrectPageSize
	}

	return nil
}
