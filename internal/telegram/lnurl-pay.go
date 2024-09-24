package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/url"
	"strconv"
	"strings"

	"github.com/massmux/SatsMobiBot/internal/network"
	"github.com/massmux/SatsMobiBot/internal/telegram/intercept"

	"github.com/massmux/SatsMobiBot/internal/errors"
	"github.com/massmux/SatsMobiBot/internal/runtime/mutex"
	"github.com/massmux/SatsMobiBot/internal/storage"

	"github.com/massmux/SatsMobiBot/internal/lnbits"
	"github.com/massmux/SatsMobiBot/internal/runtime"

	lnurl "github.com/fiatjaf/go-lnurl"
	log "github.com/sirupsen/logrus"
)

// LnurlPayState saves the state of the user for an LNURL payment
type LnurlPayState struct {
	*storage.Base
	From            *lnbits.User         `json:"from"`
	LNURLPayParams  lnurl.LNURLPayParams `json:"LNURLPayParams"`
	LNURLPayValues  lnurl.LNURLPayValues `json:"LNURLPayValues"`
	Amount          int64                `json:"amount"`
	Comment         string               `json:"comment"`
	DescriptionHash string               `json:"descriptionHash,omitempty"`
	LanguageCode    string               `json:"languagecode"`
}

// lnurlPayHandler is invoked when the first lnurl response was a lnurlpay response
// at this point, the user hasn't necessarily entered an amount yet
func (bot *TipBot) lnurlPayHandler(ctx intercept.Context, payParams *LnurlPayState) (context.Context, error) {
	m := ctx.Message()
	user := LoadUser(ctx)
	if user.Wallet == nil {
		return ctx, fmt.Errorf("user has no wallet")
	}
	// object that holds all information about the send payment
	id := fmt.Sprintf("lnurlp-%d-%s", m.Sender.ID, RandStringRunes(5))
	payParams.Base = storage.New(storage.ID(id))
	payParams.From = user
	payParams.LanguageCode = ctx.Value("publicLanguageCode").(string)

	// first we check whether an amount is present in the command
	amount, amount_err := decodeAmountFromCommand(m.Text)

	// we need to figure out whether the memo starts at position 2 or 3
	// so either /lnurl <amount> <lnurl> [memo] or /lnurl <lnurl> [memo]
	memoStartsAt := 2
	if amount_err == nil {
		// amount was present
		memoStartsAt = 3
	}
	// check if memo is present in command
	memo := GetMemoFromCommand(m.Text, memoStartsAt)
	// shorten memo to allowed length
	if len(memo) > int(payParams.LNURLPayParams.CommentAllowed) {
		memo = memo[:payParams.LNURLPayParams.CommentAllowed]
	}
	if len(memo) > 0 {
		payParams.Comment = memo
	}

	// amount is already present in the command, i.e., /lnurl <amount> <LNURL>
	// amount not in allowed range from LNURL
	if amount_err == nil &&
		(int64(amount) > (payParams.LNURLPayParams.MaxSendable/1000) || int64(amount) < (payParams.LNURLPayParams.MinSendable/1000)) &&
		(payParams.LNURLPayParams.MaxSendable != 0 && payParams.LNURLPayParams.MinSendable != 0) { // only if max and min are set
		err := fmt.Errorf("amount not in range")
		log.Warnf("[lnurlPayHandler] Error: %s", err.Error())
		bot.trySendMessage(m.Sender, fmt.Sprintf(Translate(ctx, "lnurlInvalidAmountRangeMessage"), payParams.LNURLPayParams.MinSendable/1000, payParams.LNURLPayParams.MaxSendable/1000))
		ResetUserState(user, bot)
		return ctx, err
	}
	// set also amount in the state of the user
	payParams.Amount = amount * 1000 // save as mSat

	// calculate description hash of the metadata and save it
	descriptionHash, err := bot.DescriptionHash(payParams.LNURLPayParams.Metadata, "")
	if err != nil {
		return nil, err
	}
	payParams.DescriptionHash = descriptionHash

	// add result to persistent struct
	runtime.IgnoreError(payParams.Set(payParams, bot.Bunt))

	// now we actualy check whether the amount was in the command and if not, ask for it
	if payParams.LNURLPayParams.MinSendable == payParams.LNURLPayParams.MaxSendable {
		amount = payParams.LNURLPayParams.MaxSendable / 1000
		payParams.Amount = amount * 1000 // save as mSat
	} else if amount_err != nil || amount < 1 {
		// // no amount was entered, set user state and ask for amount
		bot.askForAmount(ctx, id, "LnurlPayState", payParams.LNURLPayParams.MinSendable, payParams.LNURLPayParams.MaxSendable, m.Text)
		return ctx, nil
	}

	// We need to save the pay state in the user state so we can load the payment in the next ctx
	paramsJson, err := json.Marshal(payParams)
	if err != nil {
		log.Errorf("[lnurlPayHandler] Error: %s", err.Error())
		// bot.trySendMessage(m.Sender, err.Error())
		return ctx, err
	}
	SetUserState(user, bot, lnbits.UserHasEnteredAmount, string(paramsJson))
	// directly go to confirm
	bot.lnurlPayHandlerSend(ctx)
	return ctx, nil
}

// lnurlPayHandlerSend is invoked when the user has delivered an amount and is ready to pay
func (bot *TipBot) lnurlPayHandlerSend(ctx intercept.Context) (intercept.Context, error) {
	m := ctx.Message()
	user := LoadUser(ctx)
	if user.Wallet == nil {
		return ctx, errors.Create(errors.UserNoWalletError)
	}
	statusMsg := bot.trySendMessage(m.Sender, Translate(ctx, "lnurlGettingUserMessage"))

	// assert that user has entered an amount
	if user.StateKey != lnbits.UserHasEnteredAmount {
		log.Errorln("[lnurlPayHandlerSend] state keys don't match")
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return ctx, fmt.Errorf("wrong state key")
	}

	// read the enter amount state from user.StateData
	var enterAmountData EnterAmountStateData
	err := json.Unmarshal([]byte(user.StateData), &enterAmountData)
	if err != nil {
		log.Errorf("[lnurlPayHandlerSend] Error: %s", err.Error())
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return ctx, err
	}
	// use the enter amount state of the user to load the LNURL payment state
	tx := &LnurlPayState{Base: storage.New(storage.ID(enterAmountData.ID))}
	mutex.LockWithContext(ctx, tx.ID)
	defer mutex.UnlockWithContext(ctx, tx.ID)
	fn, err := tx.Get(tx, bot.Bunt)
	if err != nil {
		log.Errorf("[lnurlPayHandlerSend] Error: %s", err.Error())
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return ctx, err
	}
	lnurlPayState := fn.(*LnurlPayState)

	// LnurlPayState loaded

	callbackUrl, err := url.Parse(lnurlPayState.LNURLPayParams.Callback)
	if err != nil {
		log.Errorf("[lnurlPayHandlerSend] Error: %s", err.Error())
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return ctx, err
	}
	client, err := network.GetClientForScheme(callbackUrl)
	if err != nil {
		log.Errorf("[lnurlPayHandlerSend] Error: %s", err.Error())
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return ctx, err
	}
	qs := callbackUrl.Query()
	// add amount to query string
	qs.Set("amount", strconv.FormatInt(lnurlPayState.Amount, 10)) // msat
	// add comment to query string
	if len(lnurlPayState.Comment) > 0 {
		qs.Set("comment", lnurlPayState.Comment)
	}

	callbackUrl.RawQuery = qs.Encode()

	res, err := client.Get(callbackUrl.String())
	if err != nil {
		log.Errorf("[lnurlPayHandlerSend] Error: %s", err.Error())
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return ctx, err
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Errorf("[lnurlPayHandlerSend] Error: %s", err.Error())
		bot.tryEditMessage(statusMsg, Translate(ctx, "errorTryLaterMessage"))
		return ctx, err
	}

	var response2 lnurl.LNURLPayValues
	json.Unmarshal(body, &response2)
	if response2.Status == "ERROR" || len(response2.PR) < 1 {
		error_reason := "Could not receive invoice."
		if len(response2.Reason) > 0 {
			error_reason = response2.Reason
		}
		log.Errorf("[lnurlPayHandlerSend] Error in LNURLPayValues: %s", error_reason)
		bot.tryEditMessage(statusMsg, fmt.Sprintf(Translate(ctx, "lnurlPaymentFailed"), error_reason))
		return ctx, fmt.Errorf("error in LNURLPayValues: %s", error_reason)
	}

	// all good
	lnurlPayState.LNURLPayValues = response2
	// add result to persistent struct
	runtime.IgnoreError(lnurlPayState.Set(lnurlPayState, bot.Bunt))
	bot.Telegram.Delete(statusMsg)

	// store success action in context for printing after the payHandler
	ctx.Context = context.WithValue(ctx, "SuccessAction", lnurlPayState.LNURLPayValues.SuccessAction)

	m.Text = fmt.Sprintf("/pay %s", response2.PR)
	return bot.payHandler(ctx)
}

func (bot *TipBot) sendToLightningAddress(ctx intercept.Context, address string, amount int64) (intercept.Context, error) {
	m := ctx.Message()
	split := strings.Split(address, "@")
	if len(split) != 2 {
		return ctx, fmt.Errorf("lightning address format wrong")
	}
	host := strings.ToLower(split[1])
	name := strings.ToLower(split[0])

	// convert address scheme into LNURL Bech32 format
	callback := fmt.Sprintf("https://%s/.well-known/lnurlp/%s", host, name)

	log.Infof("[sendToLightningAddress] %s: callback: %s", GetUserStr(m.Sender), callback)

	lnurl, err := lnurl.LNURLEncode(callback)
	if err != nil {
		return ctx, err
	}

	if amount > 0 {
		// only when amount is given, we will also add a comment to the command
		// we do this because if the amount is not given, we will have to ask for it later
		// in the lnurl handler and we don't want to add another step where we ask for a comment
		// the command to pay to lnurl with comment is /lnurl <amount> <lnurl> <comment>
		// check if comment is presentin lnrul-p
		memo := GetMemoFromCommand(m.Text, 3)
		m.Text = fmt.Sprintf("/lnurl %d %s", amount, lnurl)
		// shorten comment to allowed length
		if len(memo) > 0 {
			m.Text = m.Text + " " + memo
		}
	} else {
		// no amount was given so we will just send the lnurl
		// this will invoke the "enter amount" dialog in the lnurl handler
		m.Text = fmt.Sprintf("/lnurl %s", lnurl)
	}
	return bot.lnurlHandler(ctx)
}
