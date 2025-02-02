package exporter

import (
	"context"
	sdk "github.com/cosmos/cosmos-sdk/types"
	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"sync"
	"time"
)

func (s *Service) DelegatorHandler(w http.ResponseWriter, r *http.Request) {
	requestStart := time.Now()

	sublogger := s.Log.With().
		Str("request-id", uuid.New().String()).
		Logger()

	validatorAddress := r.URL.Query().Get("validator_address")
	valAddress, err := sdk.ValAddressFromBech32(validatorAddress)
	if err != nil {
		sublogger.Error().
			Str("validator_address", validatorAddress).
			Err(err).
			Msg("Could not get validator address")
		return
	}

	delegatorTotalGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "cosmos_validator_delegator_total",
			Help:        "Number of delegators in validator",
			ConstLabels: s.Config.ConstLabels,
		},
		[]string{"validator_address"},
	)

	registry := prometheus.NewRegistry()
	registry.MustRegister(delegatorTotalGauge)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		sublogger.Debug().
			Str("validator_address", validatorAddress).
			Msg("Started querying delegator")
		queryStart := time.Now()

		stakingClient := stakingtypes.NewQueryClient(s.GrpcConn)
		delegatorRes, err := stakingClient.ValidatorDelegations(
			context.Background(),
			&stakingtypes.QueryValidatorDelegationsRequest{
				ValidatorAddr: valAddress.String(),
				Pagination: &querytypes.PageRequest{
					Limit: s.Config.Limit,
				},
			},
		)
		if err != nil {
			sublogger.Error().
				Str("validator_address", validatorAddress).
				Err(err).
				Msg("Could not get delegator")
			return
		}

		sublogger.Debug().
			Str("validator_address", validatorAddress).
			Float64("request-time", time.Since(queryStart).Seconds()).
			Msg("Finished querying delegators")

		delegatorTotalGauge.With(prometheus.Labels{
			"validator_address": validatorAddress,
		}).Set(float64(len(delegatorRes.DelegationResponses)))
	}()

	wg.Wait()

	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
	sublogger.Info().
		Str("method", "GET").
		Str("endpoint", "/metrics/delegator?validator_address="+validatorAddress).
		Float64("request-time", time.Since(requestStart).Seconds()).
		Msg("Request processed")
}
