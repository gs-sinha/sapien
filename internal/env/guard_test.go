package env

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

func TestCheckProduction(t *testing.T) {
	prod := domain.Environment{Name: "prod", Production: true}
	nonProd := domain.Environment{Name: "staging", Production: false}

	err := CheckProduction(prod, false)
	require.Error(t, err)
	assert.Equal(t, errs.ProductionBlocked, errs.CodeOf(err))
	assert.Equal(t, "pass --allow-production", errs.As(err).Hint)

	assert.NoError(t, CheckProduction(prod, true))
	assert.NoError(t, CheckProduction(nonProd, false))
	assert.NoError(t, CheckProduction(nonProd, true))
}
