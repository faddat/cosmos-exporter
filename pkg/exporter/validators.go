package exporter

import (
	"context"
	"encoding/hex"
	crytpocode "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/rs/zerolog/log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func (s *Service) ValidatorsHandler(w http.ResponseWriter, r *http.Request) {
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	crytpocode.RegisterInterfaces(interfaceRegistry)

	requestStart := time.Now()
	config := s.Config
	sublogger := s.Log.With().
		Str("request-id", uuid.New().String()).
		Logger()

	validatorsCommissionGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "cosmos_validators_commission",
			Help:        "Commission of the Cosmos-based blockchain validator",
			ConstLabels: config.ConstLabels,
		},
		[]string{"address", "moniker"},
	)

	validatorsStatusGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "cosmos_validators_status",
			Help:        "Status of the Cosmos-based blockchain validator",
			ConstLabels: config.ConstLabels,
		},
		[]string{"address", "moniker"},
	)

	validatorsJailedGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "cosmos_validators_jailed",
			Help:        "Jailed status of the Cosmos-based blockchain validator",
			ConstLabels: config.ConstLabels,
		},
		[]string{"address", "moniker"},
	)

	validatorsTokensGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "cosmos_validators_tokens",
			Help:        "Tokens of the Cosmos-based blockchain validator",
			ConstLabels: config.ConstLabels,
		},
		[]string{"address", "moniker", "denom"},
	)

	validatorsDelegatorSharesGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "cosmos_validators_delegator_shares",
			Help:        "Delegator shares of the Cosmos-based blockchain validator",
			ConstLabels: config.ConstLabels,
		},
		[]string{"address", "moniker", "denom"},
	)

	validatorsMinSelfDelegationGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "cosmos_validators_min_self_delegation",
			Help:        "Self declared minimum self delegation shares of the Cosmos-based blockchain validator",
			ConstLabels: config.ConstLabels,
		},
		[]string{"address", "moniker", "denom"},
	)

	validatorsMissedBlocksGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "cosmos_validators_missed_blocks",
			Help:        "Missed blocks of the Cosmos-based blockchain validator",
			ConstLabels: config.ConstLabels,
		},
		[]string{"address", "moniker"},
	)

	validatorsRankGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "cosmos_validators_rank",
			Help:        "Rank of the Cosmos-based blockchain validator",
			ConstLabels: config.ConstLabels,
		},
		[]string{"address", "moniker"},
	)

	validatorsIsActiveGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "cosmos_validators_active",
			Help:        "1 if the Cosmos-based blockchain validator is in active set, 0 if no",
			ConstLabels: config.ConstLabels,
		},
		[]string{"address", "pubkey_hash", "moniker"},
	)

	registry := prometheus.NewRegistry()
	registry.MustRegister(validatorsCommissionGauge)
	registry.MustRegister(validatorsStatusGauge)
	registry.MustRegister(validatorsJailedGauge)
	registry.MustRegister(validatorsTokensGauge)
	registry.MustRegister(validatorsDelegatorSharesGauge)
	registry.MustRegister(validatorsMinSelfDelegationGauge)
	registry.MustRegister(validatorsMissedBlocksGauge)
	registry.MustRegister(validatorsRankGauge)
	registry.MustRegister(validatorsIsActiveGauge)

	var validators []stakingtypes.Validator
	var signingInfos []slashingtypes.ValidatorSigningInfo
	var validatorSetLength uint32

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		sublogger.Debug().Msg("Started querying validators")
		queryStart := time.Now()

		stakingClient := stakingtypes.NewQueryClient(s.GrpcConn)

		offset := uint64(0)
		for {
			validatorsResponse, err := stakingClient.Validators(
				context.Background(),
				&stakingtypes.QueryValidatorsRequest{
					Pagination: &querytypes.PageRequest{
						Limit:  config.Limit,
						Offset: offset,
					},
				},
			)

			if err != nil {
				sublogger.Error().Err(err).Msg("Could not get validators")
				return
			}

			validatorsOnPage := validatorsResponse.GetValidators()
			if validatorsResponse == nil || len(validatorsOnPage) == 0 {
				break
			}
			validators = append(validators, validatorsOnPage...)
			offset = uint64(len(validators))
		}

		sublogger.Debug().
			Float64("request-time", time.Since(queryStart).Seconds()).
			Msg("Finished querying validators")

		// sorting by delegator shares to display rankings (unbonded go last)
		sort.Slice(validators, func(i, j int) bool {
			if !validators[i].IsBonded() && validators[j].IsBonded() {
				return false
			} else if validators[i].IsBonded() && !validators[j].IsBonded() {
				return true
			}

			return validators[i].DelegatorShares.BigInt().Cmp(validators[j].DelegatorShares.BigInt()) > 0
		})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		sublogger.Debug().Msg("Started querying validators signing infos")
		queryStart := time.Now()

		slashingClient := slashingtypes.NewQueryClient(s.GrpcConn)
		signingInfosResponse, err := slashingClient.SigningInfos(
			context.Background(),
			&slashingtypes.QuerySigningInfosRequest{
				Pagination: &querytypes.PageRequest{
					Limit: config.Limit,
				},
			},
		)
		if err != nil {
			sublogger.Error().
				Err(err).
				Msg("Could not get validators signing infos")
			return
		}

		sublogger.Debug().
			Float64("request-time", time.Since(queryStart).Seconds()).
			Msg("Finished querying validator signing infos")
		signingInfos = signingInfosResponse.Info
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		sublogger.Debug().Msg("Started querying staking params")
		queryStart := time.Now()

		stakingClient := stakingtypes.NewQueryClient(s.GrpcConn)
		paramsResponse, err := stakingClient.Params(
			context.Background(),
			&stakingtypes.QueryParamsRequest{},
		)
		if err != nil {
			sublogger.Error().
				Err(err).
				Msg("Could not get staking params")
			return
		}

		sublogger.Debug().
			Float64("request-time", time.Since(queryStart).Seconds()).
			Msg("Finished querying staking params")
		validatorSetLength = paramsResponse.Params.MaxValidators
	}()

	wg.Wait()

	sublogger.Info().
		Int("signingLength", len(signingInfos)).
		Int("validatorsLength", len(validators)).
		Msg("Validators info")

	activeValidators := 0
	for index, validator := range validators {
		// because cosmos's dec doesn't have .toFloat64() method or whatever and returns everything as int
		rate, err := strconv.ParseFloat(validator.Commission.CommissionRates.Rate.String(), 64)
		if err != nil {
			log.Error().
				Err(err).
				Str("address", validator.OperatorAddress).
				Msg("Could not get commission")
		} else {
			validatorsCommissionGauge.With(prometheus.Labels{
				"address": validator.OperatorAddress,
				"moniker": validator.Description.Moniker,
			}).Set(rate)
		}

		validatorsStatusGauge.With(prometheus.Labels{
			"address": validator.OperatorAddress,
			"moniker": validator.Description.Moniker,
		}).Set(float64(validator.Status))

		// golang doesn't have a ternary operator, so we have to stick with this ugly solution
		var jailed float64

		if validator.Jailed {
			jailed = 1
		} else {
			jailed = 0
		}
		validatorsJailedGauge.With(prometheus.Labels{
			"address": validator.OperatorAddress,
			"moniker": validator.Description.Moniker,
		}).Set(jailed)

		// because cosmos's dec doesn't have .toFloat64() method or whatever and returns everything as int
		if value, err := strconv.ParseFloat(validator.Tokens.String(), 64); err != nil {
			sublogger.Error().
				Str("address", validator.OperatorAddress).
				Err(err).
				Msg("Could not parse delegator tokens")
		} else {
			validatorsTokensGauge.With(prometheus.Labels{
				"address": validator.OperatorAddress,
				"moniker": validator.Description.Moniker,
				"denom":   config.Denom,
			}).Set(value / config.DenomCoefficient) // a better way to do this is using math/big Div then checking IsInt64
		}

		// because cosmos's dec doesn't have .toFloat64() method or whatever and returns everything as int
		if value, err := strconv.ParseFloat(validator.DelegatorShares.String(), 64); err != nil {
			sublogger.Error().
				Str("address", validator.OperatorAddress).
				Err(err).
				Msg("Could not parse delegator shares")
		} else {
			validatorsDelegatorSharesGauge.With(prometheus.Labels{
				"address": validator.OperatorAddress,
				"moniker": validator.Description.Moniker,
				"denom":   config.Denom,
			}).Set(value / config.DenomCoefficient)
		}

		// because cosmos's dec doesn't have .toFloat64() method or whatever and returns everything as int
		if value, err := strconv.ParseFloat(validator.MinSelfDelegation.String(), 64); err != nil {
			sublogger.Error().
				Str("address", validator.OperatorAddress).
				Err(err).
				Msg("Could not parse validator min self delegation")
		} else {
			validatorsMinSelfDelegationGauge.With(prometheus.Labels{
				"address": validator.OperatorAddress,
				"moniker": validator.Description.Moniker,
				"denom":   config.Denom,
			}).Set(value / config.DenomCoefficient)
		}

		err = validator.UnpackInterfaces(interfaceRegistry) // Unpack interfaces, to populate the Anys' cached values
		if err != nil {
			sublogger.Error().
				Str("address", validator.OperatorAddress).
				Err(err).
				Msg("Could not get unpack validator inferfaces")
		}

		pubKey, err := validator.GetConsAddr()
		if err != nil {
			sublogger.Error().
				Str("address", validator.OperatorAddress).
				Err(err).
				Msg("Could not get validator pubkey")
		}

		var signingInfo slashingtypes.ValidatorSigningInfo
		found := false

		for _, signingInfoIterated := range signingInfos {
			if pubKey.String() == signingInfoIterated.Address {
				found = true
				signingInfo = signingInfoIterated
				break
			}
		}

		if !found {
			slashingClient := slashingtypes.NewQueryClient(s.GrpcConn)
			slashingRes, err := slashingClient.SigningInfo(
				context.Background(),
				&slashingtypes.QuerySigningInfoRequest{ConsAddress: pubKey.String()},
			)
			if err != nil {
				sublogger.Debug().
					Str("address", validator.OperatorAddress).
					Msg("Could not get signing info for validator")
				continue
			}
			found = true
			signingInfo = slashingRes.ValSigningInfo
		}

		if found && (validator.Status == stakingtypes.Bonded) {
			validatorsMissedBlocksGauge.With(prometheus.Labels{
				"address": validator.OperatorAddress,
				"moniker": validator.Description.Moniker,
			}).Set(float64(signingInfo.MissedBlocksCounter))
		} else {
			sublogger.Trace().
				Str("address", validator.OperatorAddress).
				Msg("Validator is not active, not returning missed blocks amount.")
		}

		validatorsRankGauge.With(prometheus.Labels{
			"address": validator.OperatorAddress,
			"moniker": validator.Description.Moniker,
		}).Set(float64(index + 1))

		if validatorSetLength != 0 {
			// golang doesn't have a ternary operator, so we have to stick with this ugly solution
			active := float64(1)

			if validator.Jailed {
				active = 0
			}

			if activeValidators == int(validatorSetLength) {
				active = 0
			}
			activeValidators += int(active)

			validatorsIsActiveGauge.With(prometheus.Labels{
				"address":     validator.OperatorAddress,
				"moniker":     validator.Description.Moniker,
				"pubkey_hash": strings.ToUpper(hex.EncodeToString(pubKey.Bytes())),
			}).Set(active)
		}
	}
	sublogger.Info().Int("activeValidators", activeValidators).Msg("Active validators")

	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
	sublogger.Info().
		Str("method", "GET").
		Str("endpoint", "/metrics/validators").
		Float64("request-time", time.Since(requestStart).Seconds()).
		Msg("Request processed")
}
