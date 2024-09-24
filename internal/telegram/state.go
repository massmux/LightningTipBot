package telegram

import (
	"github.com/massmux/SatsMobiBot/internal/lnbits"
	"github.com/massmux/SatsMobiBot/internal/telegram/intercept"
)

type StateCallbackMessage map[lnbits.UserStateKey]func(ctx intercept.Context) (intercept.Context, error)

var stateCallbackMessage StateCallbackMessage

func initializeStateCallbackMessage(bot *TipBot) {
	stateCallbackMessage = StateCallbackMessage{
		lnbits.UserStateLNURLEnterAmount:     bot.enterAmountHandler,
		lnbits.UserEnterAmount:               bot.enterAmountHandler,
		lnbits.UserEnterUser:                 bot.enterUserHandler,
		lnbits.UserEnterShopTitle:            bot.enterShopTitleHandler,
		lnbits.UserStateShopItemSendPhoto:    bot.addShopItemPhoto,
		lnbits.UserStateShopItemSendPrice:    bot.enterShopItemPriceHandler,
		lnbits.UserStateShopItemSendTitle:    bot.enterShopItemTitleHandler,
		lnbits.UserStateShopItemSendItemFile: bot.addItemFileHandler,
		lnbits.UserEnterShopsDescription:     bot.enterShopsDescriptionHandler,
		lnbits.UserEnterDallePrompt:          bot.confirmGenerateImages,
	}
}
