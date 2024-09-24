package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/massmux/SatsMobiBot/internal/errors"
	"github.com/massmux/SatsMobiBot/internal/telegram/intercept"

	"github.com/massmux/SatsMobiBot/internal/lnbits"
	"github.com/massmux/SatsMobiBot/pkg/lightning"
	log "github.com/sirupsen/logrus"
	tb "gopkg.in/lightningtipbot/telebot.v3"
)

func (bot *TipBot) anyTextHandler(ctx intercept.Context) (intercept.Context, error) {
	m := ctx.Message()
	if m.Chat.Type != tb.ChatPrivate {
		return ctx, errors.Create(errors.NoPrivateChatError)
	}

	// check if user is in Database, if not, initialize wallet
	user := LoadUser(ctx)
	if user.Wallet == nil || !user.Initialized {
		return bot.startHandler(ctx)
	}

	// check if the user clicked on the balance button
	if strings.HasPrefix(m.Text, MainMenuCommandBalance) {
		bot.tryDeleteMessage(m)
		// overwrite the message text so it doesn't cause an infinite loop
		// because balanceHandler calls anyTextHAndler...
		m.Text = ""
		return bot.balanceHandler(ctx)
	}

	// could be an invoice
	anyText := strings.ToLower(m.Text)
	if lightning.IsInvoice(anyText) {
		m.Text = "/pay " + anyText
		return bot.payHandler(ctx)
	}
	if lightning.IsLnurl(anyText) {
		m.Text = "/lnurl " + anyText
		return bot.lnurlHandler(ctx)
	}
	if c := stateCallbackMessage[user.StateKey]; c != nil {
		return c(ctx)
		//ResetUserState(user, bot)
	}
	return ctx, nil
}

type EnterUserStateData struct {
	ID              string `json:"ID"`              // holds the ID of the tx object in bunt db
	Type            string `json:"Type"`            // holds type of the tx in bunt db (needed for type checking)
	Amount          int64  `json:"Amount"`          // holds the amount entered by the user mSat
	AmountMin       int64  `json:"AmountMin"`       // holds the minimum amount that needs to be entered mSat
	AmountMax       int64  `json:"AmountMax"`       // holds the maximum amount that needs to be entered mSat
	OiringalCommand string `json:"OiringalCommand"` // hold the originally entered command for evtl later use
}

func (bot *TipBot) askForUser(ctx context.Context, id string, eventType string, originalCommand string) (enterUserStateData *EnterUserStateData, err error) {
	user := LoadUser(ctx)
	if user.Wallet == nil {
		return // errors.New("user has no wallet"), 0
	}
	enterUserStateData = &EnterUserStateData{
		ID:              id,
		Type:            eventType,
		OiringalCommand: originalCommand,
	}
	// set LNURLPayParams in the state of the user
	stateDataJson, err := json.Marshal(enterUserStateData)
	if err != nil {
		log.Errorln(err)
		return
	}
	SetUserState(user, bot, lnbits.UserEnterUser, string(stateDataJson))
	// Let the user enter a user and return
	bot.trySendMessage(user.Telegram, Translate(ctx, "enterUserMessage"), tb.ForceReply)
	return
}

// enterAmountHandler is invoked in anyTextHandler when the user needs to enter an amount
// the amount is then stored as an entry in the user's stateKey in the user database
// any other ctx that relies on this, needs to load the resulting amount from the database
func (bot *TipBot) enterUserHandler(ctx intercept.Context) (intercept.Context, error) {
	m := ctx.Message()
	user := LoadUser(ctx)
	if user.Wallet == nil {
		return ctx, errors.Create(errors.UserNoWalletError)
	}

	if !(user.StateKey == lnbits.UserEnterUser) {
		ResetUserState(user, bot)
		return ctx, errors.Create(errors.InvalidSyntaxError)
	}
	if len(m.Text) < 4 || strings.HasPrefix(m.Text, "/") || m.Text == SendMenuCommandEnter {
		ResetUserState(user, bot)
		return ctx, errors.Create(errors.InvalidSyntaxError)
	}

	var EnterUserStateData EnterUserStateData
	err := json.Unmarshal([]byte(user.StateData), &EnterUserStateData)
	if err != nil {
		log.Errorf("[EnterUserHandler] %s", err.Error())
		ResetUserState(user, bot)
		return ctx, err
	}

	userstr := m.Text

	// find out which type the object in bunt has waiting for an amount
	// we stored this in the EnterAmountStateData before
	switch EnterUserStateData.Type {
	case "CreateSendState":
		m.Text = fmt.Sprintf("/send %s", userstr)
		return bot.sendHandler(ctx)
	default:
		ResetUserState(user, bot)
		return ctx, errors.Create(errors.InvalidSyntaxError)
	}
	return ctx, nil
}
