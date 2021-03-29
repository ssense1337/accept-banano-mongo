package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/tigwyk/accept-banano/internal/maplock"
	"github.com/tigwyk/accept-banano/internal/banano"
	"github.com/tigwyk/accept-banano/internal/units"
	"github.com/cenkalti/log"
	"github.com/shopspring/decimal"
	"go.etcd.io/bbolt"
)

var (
	errPaymentNotFound     = errors.New("payment not found")
	errPaymentNotFulfilled = errors.New("payment not fulfilled")
)

var notificationClient http.Client

// Payment is the data type stored in the database in JSON format.
type Payment struct {
	// Customer sends money to this account.
	account string
	// Index for generating deterministic key.
	Index string `json:"index"`
	// Currency of amount in original request.
	Currency string `json:"currency"`
	// Original amount requested by client in preferred currency.
	AmountInCurrency decimal.Decimal `json:"amountInCurrency"`
	// Requested amount in raw.
	// Calculated when payment request is created.
	// Payment is fulfilled when Account contains at least this amount.
	Amount decimal.Decimal `json:"amount"`
	// Current balance of Account in raw.
	Balance decimal.Decimal `json:"balance"`
	// Individual transactions to pay the total amount.
	SubPayments map[string]SubPayment `json:"subPayments"`
	// Free text field to pass from customer to merchant.
	State string `json:"state"`
	// Set when customer created the payment request via API.
	CreatedAt time.Time `json:"createdAt"`
	// Set every time Account is checked for incoming funds.
	LastCheckedAt *time.Time `json:"lastCheckedAt"`
	// Set when detected customer has sent enough funds to Account.
	FulfilledAt *time.Time `json:"fulfilledAt"`
	// Set when merchant is notified.
	NotifiedAt *time.Time `json:"notifiedAt"`
	// Set when pending funds are accepted to Account.
	ReceivedAt *time.Time `json:"receivedAt"`
	// Set when Amount is sent to the merchant account.
	SentAt *time.Time `json:"sentAt"`
}

type SubPayment struct {
	// Amount in raw.
	Amount decimal.Decimal `json:"amount"`
	// Sender account.
	Account string `json:"account"`
}

// LoadPayment fetches a Payment object from database by key.
func LoadPayment(account string) (*Payment, error) {
	var value []byte
	err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(paymentsBucket))
		v := b.Get([]byte(account))
		if v == nil {
			return nil
		}
		value = make([]byte, len(v))
		copy(value, v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errPaymentNotFound
	}
	payment := &Payment{account: account}
	err = json.Unmarshal(value, payment)
	return payment, err
}

func LoadActivePayments() ([]*Payment, error) {
	ret := make([]*Payment, 0)
	err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(paymentsBucket))
		return b.ForEach(func(k, v []byte) error {
			p := &Payment{account: string(k)}
			err := json.Unmarshal(v, p)
			if err != nil {
				log.Error(err)
				return nil
			}
			if !p.finished() {
				ret = append(ret, p)
			}
			return nil
		})
	})
	return ret, err
}

// Save the Payment object in database.
func (p *Payment) Save() error {
	value, err := json.Marshal(&p)
	if err != nil {
		return err
	}
	return db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(paymentsBucket))
		return b.Put([]byte(p.account), value)
	})
}

// SaveNew saves newly created payment. Sets account and index fields before saving.
func (p *Payment) SaveNew() error {
	return db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(paymentsBucket))
		// Before using incremental ids, payment accounts were being generated with random indices.
		// It is very unlikely that there are many payments with consecutive indices saved in database.
		var index string
		var account string
		found := false
		for i := 0; i < 100; i++ {
			// This returns an error only if the Tx is closed or not writeable.
			// That can't happen in an Update() call so I ignore the error check.
			id, _ := b.NextSequence()
			index = strconv.FormatUint(id, 10)
			key, err := node.DeterministicKey(config.Seed, index)
			if err != nil {
				return err
			}
			v := b.Get([]byte(key.Account))
			if v != nil {
				// Payment exists with generated incremental index. Try next index.
				continue
			}
			found = true
			account = key.Account
			break
		}
		if !found {
			return errors.New("internal error: cannot create unique index")
		}
		p.Index = index
		p.account = account
		value, err := json.Marshal(&p)
		if err != nil {
			return err
		}
		return b.Put([]byte(p.account), value)
	})
}

// NextCheck returns the next timestamp payment should be checked at.
func (p Payment) NextCheck() time.Duration {
	if p.LastCheckedAt == nil {
		return config.MinNextCheckDuration
	}
	create := p.CreatedAt
	lastCheck := *p.LastCheckedAt

	now := time.Now().UTC()
	minWait := config.MinNextCheckDuration
	maxWait := config.MaxNextCheckDuration
	passed := now.Sub(create)
	nextWait := passed / time.Duration(config.NextCheckDurationFactor)
	if nextWait < minWait {
		nextWait = minWait
	} else if nextWait > maxWait {
		nextWait = maxWait
	}
	nextCheck := lastCheck.Add(nextWait)
	return nextCheck.Sub(now)
}

// finished returns true after all operations are complete or allowed duration for payment is passed.
func (p Payment) finished() bool {
	return p.SentAt != nil || now().Sub(p.CreatedAt) > config.AllowedDuration
}

func (p Payment) remainingDuration() time.Duration {
	return p.CreatedAt.Add(config.AllowedDuration).Sub(*now())
}

// StartChecking starts a goroutine to check the payment periodically.
func (p *Payment) StartChecking() {
	if p.finished() {
		return
	}
	checkPaymentWG.Add(1)
	go p.checkLoop()
}

func (p *Payment) checkLoop() {
	defer checkPaymentWG.Done()

	if subs != nil {
		subs.Subscribe(p.account)
		defer subs.Unsubscribe(p.account)
	}

	for {
		if p.finished() {
			return
		}
		select {
		case <-time.After(p.NextCheck()):
			p.checkOnce()
		case <-stopCheckPayments:
			return
		}
	}
}

func (p *Payment) checkOnce() {
	locks.Lock(p.account)
	defer locks.Unlock(p.account)

	err := p.reload()
	if err != nil {
		log.Errorln("cannot load payment:", p.account)
		return
	}
	err = p.check()
	if err != nil {
		log.Errorf("error checking %s: %s", p.account, err)
		return
	}
}

// Reload payment because it might be updated by admin operations.
func (p *Payment) reload() error {
	p2, err := LoadPayment(p.account)
	if err != nil {
		return err
	}
	*p = *p2
	return nil
}

func (p *Payment) check() error {
	log.Debugln("checking payment:", p.account)
	err := p.process()
	p.LastCheckedAt = now()
	switch err {
	case errPaymentNotFulfilled:
		log.Debug(err)
		return p.Save()
	case nil:
		return p.Save()
	default:
		if err2 := p.Save(); err2 != nil {
			log.Errorln("cannot save payment:", err2)
		}
		return err
	}
}

var locks = maplock.New()

func (p *Payment) process() error { // nolint: gocognit
	if p.SentAt == nil { // nolint: nestif
		if p.ReceivedAt == nil {
			if p.NotifiedAt == nil {
				if p.FulfilledAt == nil {
					err := p.checkPending()
					if err != nil {
						return err
					}
					p.FulfilledAt = now()
					err = p.Save()
					if err != nil {
						return err
					}
					go verifications.Publish(PaymentVerified{Payment: *p})
				}
				err := p.notifyMerchant()
				if err != nil {
					return err
				}
				p.NotifiedAt = now()
				err = p.Save()
				if err != nil {
					return err
				}
				go verifications.Publish(PaymentVerified{Payment: *p})
			}
			err := p.receivePending()
			if err != nil {
				return err
			}
			p.ReceivedAt = now()
			err = p.Save()
			if err != nil {
				return err
			}
		}
		err := p.sendToMerchant()
		if err != nil {
			return err
		}
		p.SentAt = now()
		err = p.Save()
		if err != nil {
			return err
		}
	}
	return nil
}

func now() *time.Time {
	t := time.Now().UTC()
	return &t
}

func (p *Payment) checkPending() error {
	var totalAmount decimal.Decimal
	accountInfo, err := node.AccountInfo(p.account)
	switch err {
	case nano.ErrAccountNotFound:
	case nil:
		totalAmount = accountInfo.Balance
	default:
		return err
	}
	pendingBlocks, err := node.Pending(p.account, config.MaxPayments, units.BananoToRaw(config.ReceiveThreshold))
	if err != nil {
		return err
	}
	if len(pendingBlocks) == 0 {
		return errPaymentNotFulfilled
	}
	for hash, pendingBlock := range pendingBlocks {
		log.Debugf("received new block: %#v", hash)
		log.Debugln("amount:", units.RawToBanano(pendingBlock.Amount))
		totalAmount = totalAmount.Add(pendingBlock.Amount)
		if p.SubPayments == nil {
			p.SubPayments = make(map[string]SubPayment, 1)
		}
		p.SubPayments[hash] = SubPayment{
			Account: pendingBlock.Source,
			Amount:  pendingBlock.Amount,
		}
	}
	log.Debugln("total amount:", units.RawToBanano(totalAmount))
	if !p.Balance.Equal(totalAmount) {
		p.Balance = totalAmount
		err = p.Save()
		if err != nil {
			return err
		}
	}
	if !p.isFulfilled() {
		return errPaymentNotFulfilled
	}
	return nil
}

func (p *Payment) isFulfilled() bool {
	if !config.UnderPaymentToleranceFixed.IsZero() {
		tolerance := units.BananoToRaw(config.UnderPaymentToleranceFixed)
		if p.Balance.GreaterThanOrEqual(p.Amount.Sub(tolerance)) {
			return true
		}
	}
	if config.UnderPaymentTolerancePercent != 0 {
		percent := decimal.NewFromFloat(config.UnderPaymentTolerancePercent)
		tolerance := p.Amount.Mul(percent)
		if p.Balance.GreaterThanOrEqual(p.Amount.Sub(tolerance)) {
			return true
		}
	}
	return p.Balance.GreaterThanOrEqual(p.Amount)
}

func (p *Payment) receivePending() error {
	pendingBlocks, err := node.Pending(p.account, config.MaxPayments, units.BananoToRaw(config.ReceiveThreshold))
	if err != nil {
		return err
	}
	if len(pendingBlocks) == 0 {
		return nil
	}
	key, err := node.DeterministicKey(config.Seed, p.Index)
	if err != nil {
		return err
	}
	for hash, pendingBlock := range pendingBlocks {
		err = receiveBlock(hash, pendingBlock.Amount, p.account, key.Private, key.Public)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Payment) sendToMerchant() error {
	key, err := node.DeterministicKey(config.Seed, p.Index)
	if err != nil {
		return err
	}
	return sendAll(p.account, config.Account, key.Private)
}

func (p *Payment) notifyMerchant() error {
	if config.NotificationURL == "" {
		return nil
	}
	notification := Notification{
		Account:          p.account,
		Amount:           units.RawToBanano(p.Amount),
		AmountInCurrency: p.AmountInCurrency,
		Currency:         p.Currency,
		Balance:          units.RawToBanano(p.Balance),
		State:            p.State,
		Fulfilled:        p.FulfilledAt != nil,
		FulfilledAt:      p.FulfilledAt,
	}
	data, err := json.Marshal(notification)
	if err != nil {
		return err
	}
	resp, err := notificationClient.Post(config.NotificationURL, "application/json", bytes.NewReader(data)) // nolint:noctx // client timeout set
	if err != nil {
		return err
	}
	defer func() {
		if err2 := resp.Body.Close(); err2 != nil {
			log.Debug(err2)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return errors.New("bad notification response")
	}
	return nil
}
