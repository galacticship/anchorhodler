package internal

import (
	"context"

	"github.com/galacticship/terra"
	"github.com/galacticship/terra/cosmos"
	"github.com/galacticship/terra/protocols/anchor"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

type AnchorHodler struct {
	querier *terra.Querier
	wallet  *terra.Wallet

	anc *anchor.Anchor
}

func NewAnchorHodler(querier *terra.Querier, wallet *terra.Wallet) (*AnchorHodler, error) {
	anc, err := anchor.NewAnchor(querier)
	if err != nil {
		return nil, errors.Wrap(err, "creating anchor object")
	}
	return &AnchorHodler{
		querier: querier,
		wallet:  wallet,
		anc:     anc,
	}, nil
}

func (h *AnchorHodler) CheckLtv(ctx context.Context, minLtv float64, maxLtv float64, targetLtv float64) error {
	currentLtv, err := h.GetLtv(ctx)
	if err != nil {
		return errors.Wrap(err, "getting currentLtv")
	}
	log.Info().Msgf("ltv: %s", currentLtv.StringFixed(2))
	needProcess := false
	if currentLtv.LessThan(decimal.NewFromFloat(minLtv)) {
		log.Info().Msgf("current ltv (%s) is less than minimum ltv (%.2f) -> borrowing to target ltv (%.2f)", currentLtv, minLtv, targetLtv)
		needProcess = true
	}
	if currentLtv.GreaterThan(decimal.NewFromFloat(maxLtv)) {
		log.Info().Msgf("current ltv (%s) is greater than maximum ltv (%.2f) -> repaying to target ltv (%.2f)", currentLtv, maxLtv, targetLtv)
		needProcess = true
	}
	if needProcess {
		err := h.SetLtv(ctx, decimal.NewFromFloat(targetLtv))
		if err != nil {
			return errors.Wrap(err, "borrowing to target ltv")
		}
	}
	return nil
}

func (h *AnchorHodler) GetLtv(ctx context.Context) (decimal.Decimal, error) {
	borrowLimit, err := h.anc.Overseer.BorrowLimit(ctx, h.wallet.Address())
	if err != nil {
		return decimal.Zero, errors.Wrap(err, "getting borrowLimit")
	}
	borrowerInfo, err := h.anc.Market.BorrowerInfo(ctx, h.wallet.Address())
	if err != nil {
		return decimal.Zero, errors.Wrap(err, "getting borrower info")
	}
	return borrowerInfo.LoanAmount.Div(borrowLimit).Mul(decimal.NewFromInt(100)), nil
}

func (h *AnchorHodler) SetLtv(ctx context.Context, ltv decimal.Decimal) error {
	borrowLimit, err := h.anc.Overseer.BorrowLimit(ctx, h.wallet.Address())
	if err != nil {
		return errors.Wrap(err, "getting borrowLimit")
	}
	borrowerInfo, err := h.anc.Market.BorrowerInfo(ctx, h.wallet.Address())
	if err != nil {
		return errors.Wrap(err, "getting borrower info")
	}
	newLoanAmount := borrowLimit.Mul(ltv).Div(decimal.NewFromInt(100)).Truncate(6)
	log.Info().Msgf("new loan amount: %s UST", newLoanAmount.StringFixed(2))
	diff := newLoanAmount.Sub(borrowerInfo.LoanAmount)
	log.Info().Msgf("diff with current loan: %s", diff.StringFixed(2))

	if diff.Equal(decimal.Zero) {
		log.Info().Msg("ltv is already at the asked value")
		return nil
	}

	if diff.GreaterThan(decimal.Zero) {
		log.Info().Msg("borrowing & depositing to anchor...")
		err = terra.NewTransaction(h.querier).
			Message(func() (cosmos.Msg, error) {
				return h.anc.Market.NewBorrowStableMessage(h.wallet.Address(), diff)
			}).Message(func() (cosmos.Msg, error) {
			return h.anc.Market.NewDepositUSTMessage(h.wallet.Address(), diff)
		}).ExecuteAndWaitFor(ctx, h.wallet)
		if err != nil {
			return errors.Wrap(err, "executing multiple transactions to set ltv")
		}
	} else {
		log.Info().Msg("redeeming AUST & repaying loan...")
		usttorepay := diff.Abs()
		epochState, err := h.anc.Market.EpochState(ctx)
		if err != nil {
			return errors.Wrap(err, "getting epoch state of market")
		}
		austToRepay := usttorepay.Div(epochState.ExchangeRate).Truncate(6)
		log.Info().Msgf("AUST to repay: %s (exchange rate: %s)", austToRepay.StringFixed(2), epochState.ExchangeRate)
		austBalance, err := terra.AUST.Balance(ctx, h.querier, h.wallet.Address())
		if err != nil {
			return errors.Wrap(err, "getting AUST balance")
		}
		log.Info().Msgf("AUST balance: %s", austBalance.StringFixed(2))
		if austBalance.LessThan(austToRepay) {
			return errors.Errorf("not enough AUST in wallet (balance %s / needed %s)", austBalance, austToRepay)
		}
		err = terra.NewTransaction(h.querier).
			Message(func() (cosmos.Msg, error) {
				return h.anc.Market.NewRedeemAUSTMessage(h.wallet.Address(), austToRepay)
			}).Message(func() (cosmos.Msg, error) {
			return h.anc.Market.NewRepayStableMessage(h.wallet.Address(), usttorepay)
		}).ExecuteAndWaitFor(ctx, h.wallet)
		if err != nil {
			return errors.Wrap(err, "executing multiple transactions to set ltv")
		}
	}

	log.Info().Msgf("ltv set to %s", ltv)
	return nil
}
