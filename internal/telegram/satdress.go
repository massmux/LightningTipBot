package telegram

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/massmux/SatsMobiBot/internal/runtime"
	"github.com/massmux/SatsMobiBot/internal/telegram/intercept"
	tb "gopkg.in/lightningtipbot/telebot.v3"

	"github.com/massmux/SatsMobiBot/internal/lnbits"
	"github.com/massmux/SatsMobiBot/internal/satdress"
	"github.com/massmux/SatsMobiBot/internal/storage"
	"github.com/eko/gocache/store"
	log "github.com/sirupsen/logrus"
	"github.com/skip2/go-qrcode"
)

var (
	registerNodeMessage            = "📖 Connect your Lightning node with your wallet.\n\nCurrently supported backends: `lnd` and `lnbits`\nTo register a node, type: `/node add <type> <info>`\n\n*LND (REST):* `/node add lnd <host> <macaroon> <cert>`\n*LNbits:* `/node add lnbits <host> <key>`\n\nℹ️ Always use `https://` for the `<host>`, even if you use a Tor node. Certificates and macaroons need to be in base64 format.\n\n⚠️ For security reasons, you should *only use an invoice macaroon* for LND and an *invoice key* for LNbits."
	nodeHelpMessage                = "⚙️ *Commands:*\n`/node add <type> <info>` ✅ Add your node.\n`/node invoice <admount>` ⤵️ Fetch an invoice from your node.\n`/node proxy <amount>` 🔀 Proxy a payment to your node (privacy feature).\n`/node help` 📖 Show help."
	checkingInvoiceMessage         = "⏳ Checking invoice on your node..."
	invoiceNotSettledMessage       = "❌ Invoice has not settled yet."
	checkInvoiceButtonMessage      = "🔄 Check invoice"
	routingInvoiceMessage          = "🔄 Getting invoice from your node..."
	checkingNodeMessage            = "🔄 Checking your node..."
	errorCouldNotAddNodeMessage    = "❌ Could not add node. Please check your node details."
	gettingInvoiceOnlyErrorMessage = "❌ Error getting invoice from your node."
	gettingInvoiceErrorMessage     = "❌ Error getting invoice from your node. Your funds are still available."
	payingInvoiceErrorMessage      = "❌ Could not route payment. Your funds are still available."
	invoiceRoutedMessage           = "✅ *Payment routed to your node.*"
	invoiceSettledMessage          = "✅ *Invoice settled.*"
	nodeAddedMessage               = "✅ *Node added.*"
	satdressCheckInvoicenMenu      = &tb.ReplyMarkup{ResizeKeyboard: true}
	btnSatdressCheckInvoice        = satdressCheckInvoicenMenu.Data(checkInvoiceButtonMessage, "satdress_check_invoice")
)

// todo -- rename to something better like parse node settings or something
func parseUserSettingInput(ctx intercept.Context, m *tb.Message) (satdress.BackendParams, error) {
	// input is "/node add <Type> <Host> <Macaroon> <Cert>"
	params := satdress.LNDParams{}
	splits := strings.Split(m.Text, " ")
	splitlen := len(splits)
	if splitlen < 4 {
		return params, fmt.Errorf("not enough arguments")
	}
	switch strings.ToLower(splits[2]) {
	case "lnd":
		if splitlen < 5 || splitlen > 7 {
			return params, fmt.Errorf("wrong format. Use <Type> <Host> <Macaroon> [<Cert>]")
		}
		host := splits[3]
		macaroon := splits[4]
		var pem []byte
		if splitlen == 6 {
			cert := splits[5]
			pem := parseCertificateToPem(cert)
			if len(pem) < 1 {
				return params, fmt.Errorf("certificate has invalid format")
			}
		}

		hostsplit := strings.Split(host, ".")
		if len(hostsplit) == 0 {
			return params, fmt.Errorf("host has wrong format")
		}

		return satdress.LNDParams{
			Cert:       pem,
			Host:       host,
			Macaroon:   macaroon,
			CertString: string(pem),
		}, nil
	case "lnbits":
		if splitlen < 5 || splitlen > 6 {
			return params, fmt.Errorf("wrong format. Use <Type> <Host> <Key>")
		}
		host := splits[3]
		key := splits[4]

		host = strings.TrimSuffix(host, "/")
		hostsplit := strings.Split(host, ".")
		if len(hostsplit) == 0 {
			return params, fmt.Errorf("host has wrong format")
		}
		return satdress.LNBitsParams{
			Host: host,
			Key:  key,
		}, nil
	default:
		return params, fmt.Errorf("unknown backend type. Supported types: `lnd`, `lnbits`")
	}
}

func nodeInfoString(node *lnbits.NodeSettings) (string, error) {
	if len(node.NodeType) == 0 {
		return "", fmt.Errorf("node type is empty")
	}
	var node_info_str_filled string
	var node_info_str string
	switch strings.ToLower(node.NodeType) {
	case "lnd":
		node_info_str = "*Type:* `%s`\n\n*Host:*\n\n`%s`\n\n*Macaroon:*\n\n`%s`\n\n*Cert:*\n\n`%s`"
		node_info_str_filled = fmt.Sprintf(node_info_str, node.NodeType, node.LNDParams.Host, node.LNDParams.Macaroon, node.LNDParams.CertString)
	case "lnbits":
		node_info_str = "*Type:* `%s`\n\n*Host:*\n\n`%s`\n\n*Key:*\n\n`%s`"
		node_info_str_filled = fmt.Sprintf(node_info_str, node.NodeType, node.LNbitsParams.Host, node.LNbitsParams.Key)
	default:
		return "", fmt.Errorf("unknown node type")
	}
	return fmt.Sprintf("ℹ️ *Your node information.*\n\n%s", node_info_str_filled), nil
}

func (bot *TipBot) nodeHelpHandler(ctx intercept.Context) (intercept.Context, error) {
	bot.trySendMessage(ctx.Message().Sender, registerNodeMessage+"\n\n"+nodeHelpMessage)
	return ctx, nil
}

func (bot *TipBot) getNodeHandler(ctx intercept.Context) (intercept.Context, error) {
	m := ctx.Message()
	user, err := GetLnbitsUserWithSettings(m.Sender, *bot)
	if err != nil {
		log.Infof("Could not get user settings for user %s", GetUserStr(user.Telegram))
		return ctx, err
	}

	if user.Settings == nil {
		bot.trySendMessage(m.Sender, registerNodeMessage+"\n\n"+nodeHelpMessage)
		return ctx, fmt.Errorf("no node registered")
	}

	node_info_str, err := nodeInfoString(&user.Settings.Node)
	if err != nil {
		log.Infof("Could not get node info for user %s", GetUserStr(user.Telegram))
		bot.trySendMessage(m.Sender, registerNodeMessage+"\n\n"+nodeHelpMessage)
		return ctx, err
	}
	bot.trySendMessage(m.Sender, node_info_str)

	return ctx, nil
}

func (bot *TipBot) nodeHandler(ctx intercept.Context) (intercept.Context, error) {
	m := ctx.Message()
	splits := strings.Split(m.Text, " ")
	if len(splits) == 1 {
		return bot.getNodeHandler(ctx)
	} else if len(splits) > 1 {
		switch strings.ToLower(splits[1]) {
		case "invoice":
			return bot.invHandler(ctx)
		case "add":
			return bot.registerNodeHandler(ctx)
		case "check":
			return bot.satdressCheckInvoiceHandler(ctx)
		case "proxy":
			return bot.satdressProxyHandler(ctx)
		case "help":
			return bot.nodeHelpHandler(ctx)
		}
	}
	return ctx, nil
}

func (bot *TipBot) registerNodeHandler(ctx intercept.Context) (intercept.Context, error) {
	m := ctx.Message()
	user, err := GetLnbitsUserWithSettings(m.Sender, *bot)
	if err != nil {
		return ctx, err
	}
	check_message := bot.trySendMessageEditable(user.Telegram, checkingNodeMessage)

	backendParams, err := parseUserSettingInput(ctx, m)
	if err != nil {
		bot.tryEditMessage(check_message, fmt.Sprintf(Translate(ctx, "errorReasonMessage"), err.Error()))
		return ctx, err
	}

	switch backend := backendParams.(type) {
	case satdress.LNDParams:
		// get test invoice from user's node
		getInvoiceParams, err := satdress.MakeInvoice(
			satdress.Params{
				Backend:     backend,
				Msatoshi:    1000,
				Description: "Test invoice",
			},
		)
		if err != nil {
			log.Errorf("[registerNodeHandler] Could not add user %s's %s node: %s", GetUserStr(user.Telegram), getInvoiceParams.Status, err.Error())
			bot.tryEditMessage(check_message, errorCouldNotAddNodeMessage)
			return ctx, err
		}

		// save node in db
		user.Settings.Node.LNDParams = &backend
		user.Settings.Node.NodeType = "lnd"
	case satdress.LNBitsParams:
		// get test invoice from user's node
		getInvoiceParams, err := satdress.MakeInvoice(
			satdress.Params{
				Backend:     backend,
				Msatoshi:    1000,
				Description: "Test invoice",
			},
		)
		if err != nil {
			log.Errorf("[registerNodeHandler] Could not add user %s's %s node: %s", GetUserStr(user.Telegram), getInvoiceParams.Status, err.Error())
			bot.tryEditMessage(check_message, errorCouldNotAddNodeMessage)
			return ctx, err
		}
		// save node in db
		user.Settings.Node.LNbitsParams = &backend
		user.Settings.Node.NodeType = "lnbits"

	}
	err = UpdateUserRecord(user, *bot)
	if err != nil {
		log.Errorf("[registerNodeHandler] could not update record of user %s: %v", GetUserStr(user.Telegram), err)
		return ctx, err
	}
	node_info_str, err := nodeInfoString(&user.Settings.Node)
	if err != nil {
		log.Infof("Could not get node info for user %s", GetUserStr(user.Telegram))
		bot.trySendMessage(m.Sender, registerNodeMessage+"\n\n"+nodeHelpMessage)
		return ctx, err
	}
	bot.tryEditMessage(check_message, fmt.Sprintf("%s\n\n%s", node_info_str, nodeAddedMessage))

	log.Infof("[node:add] Added node of user %s backend %s", GetUserStr(user.Telegram), user.Settings.Node.NodeType)
	return ctx, nil
}

func (bot *TipBot) invHandler(ctx intercept.Context) (intercept.Context, error) {
	m := ctx.Message()
	user, err := GetLnbitsUserWithSettings(m.Sender, *bot)
	if err != nil {
		return ctx, err
	}
	if user.Settings == nil || user.Settings.Node.NodeType == "" {
		bot.trySendMessage(m.Sender, "You did not register a node yet.")
		return ctx, fmt.Errorf("node of user %s not registered", GetUserStr(user.Telegram))
	}

	var amount int64
	if amount_str, err := getArgumentFromCommand(m.Text, 2); err == nil {
		amount, err = GetAmount(amount_str)
		if err != nil {
			return ctx, err
		}
	}

	log.Infof("[node:invoice] Getting invoice for user %s backend %s", GetUserStr(user.Telegram), user.Settings.Node.NodeType)

	check_message := bot.trySendMessageEditable(user.Telegram, routingInvoiceMessage)
	var getInvoiceParams satdress.CheckInvoiceParams

	switch user.Settings.Node.NodeType {
	case "lnd":
		// get invoice from user's node
		getInvoiceParams, err = satdress.MakeInvoice(
			satdress.Params{
				Backend: satdress.LNDParams{
					Cert:     []byte(user.Settings.Node.LNDParams.CertString),
					Host:     user.Settings.Node.LNDParams.Host,
					Macaroon: user.Settings.Node.LNDParams.Macaroon,
				},
				Msatoshi:    amount * 1000,
				Description: fmt.Sprintf("Invoice by %s", GetUserStr(bot.Telegram.Me)),
			},
		)
	case "lnbits":
		// get invoice from user's node
		getInvoiceParams, err = satdress.MakeInvoice(
			satdress.Params{
				Backend: satdress.LNBitsParams{
					Key:  user.Settings.Node.LNbitsParams.Key,
					Host: user.Settings.Node.LNbitsParams.Host,
				},
				Msatoshi:    amount * 1000,
				Description: fmt.Sprintf("Invoice by %s", GetUserStr(bot.Telegram.Me)),
			},
		)
	default:
		return ctx, fmt.Errorf("unknown node type %s", user.Settings.Node.NodeType)
	}
	if err != nil {
		log.Errorln(err.Error())
		bot.tryEditMessage(check_message, gettingInvoiceOnlyErrorMessage)
		return ctx, err
	}

	// bot.trySendMessage(m.Sender, fmt.Sprintf("PR: `%s`\n\nHash: `%s`\n\nStatus: `%s`", getInvoiceParams.PR, string(getInvoiceParams.Hash), getInvoiceParams.Status))

	// create qr code
	qr, err := qrcode.Encode(getInvoiceParams.PR, qrcode.Medium, 256)
	if err != nil {
		errmsg := fmt.Sprintf("[/invoice] Failed to create QR code for invoice: %s", err.Error())
		bot.trySendMessage(user.Telegram, Translate(ctx, "errorTryLaterMessage"))
		log.Errorln(errmsg)
		return ctx, err
	}
	bot.trySendMessage(m.Sender, &tb.Photo{File: tb.File{FileReader: bytes.NewReader(qr)}, Caption: fmt.Sprintf("`%s`", getInvoiceParams.PR)})

	// add the getInvoiceParams to cache to check it later
	bot.Cache.Set(fmt.Sprintf("invoice:%d", user.Telegram.ID), getInvoiceParams, &store.Options{Expiration: 24 * time.Hour})

	log.Infof("[node:invoice] Invoice created for user %s backend %s", GetUserStr(user.Telegram), user.Settings.Node.NodeType)

	// check if invoice settles
	return bot.satdressCheckInvoiceHandler(ctx)
}

func (bot *TipBot) satdressCheckInvoiceHandler(ctx intercept.Context) (intercept.Context, error) {
	tgUser := LoadUser(ctx).Telegram
	user, err := GetLnbitsUserWithSettings(tgUser, *bot)
	if err != nil {
		return ctx, err
	}

	// get the getInvoiceParams from cache
	log.Debugf("[Cache] Getting key: %s", fmt.Sprintf("invoice:%d", user.Telegram.ID))
	getInvoiceParamsInterface, err := bot.Cache.Get(fmt.Sprintf("invoice:%d", user.Telegram.ID))
	if err != nil {
		log.Errorf("[satdressCheckInvoiceHandler] UserID: %d,  %s", user.Telegram.ID, err.Error())
		return ctx, err
	}
	getInvoiceParams := getInvoiceParamsInterface.(satdress.CheckInvoiceParams)

	// check the invoice

	// check if there is an invoice check message in cache already
	check_message_interface, err := bot.Cache.Get(fmt.Sprintf("invoice:msg:%s", getInvoiceParams.Hash))
	var check_message *tb.Message
	if err != nil {
		// send a new message if there isn't one in the cache
		check_message = bot.trySendMessageEditable(tgUser, checkingInvoiceMessage)
	} else {
		check_message = check_message_interface.(*tb.Message)
		check_message, err = bot.tryEditMessage(check_message, checkingInvoiceMessage)
		if err != nil {
			log.Errorf("[satdressCheckInvoiceHandler] UserID: %d,  %s", user.Telegram.ID, err.Error())
		}
	}

	// save it in the cache for another call later
	bot.Cache.Set(fmt.Sprintf("invoice:msg:%s", getInvoiceParams.Hash), check_message, &store.Options{Expiration: 24 * time.Hour})

	deadLineCtx, cancel := context.WithDeadline(ctx, time.Now().Add(time.Second*60))
	runtime.NewRetryTicker(deadLineCtx, "node_invoice_check", runtime.WithRetryDuration(5*time.Second)).Do(func() {
		// get invoice from user's node
		log.Debugf("[satdressCheckInvoiceHandler] Checking invoice: %s", getInvoiceParams.Hash)
		getInvoiceParams, err = satdress.CheckInvoice(getInvoiceParams)
		if err != nil {
			log.Errorln(err.Error())
			return
		}
		if getInvoiceParams.Status == "SETTLED" {
			log.Debugf("[satdressCheckInvoiceHandler] Invoice settled: %s", getInvoiceParams.Hash)
			bot.tryEditMessage(check_message, invoiceSettledMessage)
			cancel()
		}

	}, func() {
		// cancel
	},
		func() {
			// deadline
			log.Debugf("[satdressCheckInvoiceHandler] Invoice check expired: %s", getInvoiceParams.Hash)
			bot.tryEditMessage(check_message, invoiceNotSettledMessage,
				&tb.ReplyMarkup{
					InlineKeyboard: [][]tb.InlineButton{
						{tb.InlineButton{Text: checkInvoiceButtonMessage, Unique: "satdress_check_invoice"}},
					},
				})
		},
	)

	return ctx, nil
}

func parseCertificateToPem(cert string) []byte {
	block, _ := pem.Decode([]byte(cert))
	if block != nil {
		// already PEM
		return []byte(cert)
	} else {
		var dec []byte

		dec, err := hex.DecodeString(cert)
		if err != nil {
			// not HEX
			dec, err = base64.StdEncoding.DecodeString(cert)
			if err != nil {
				// not base54, we have a problem huston
				return nil
			}
		}
		if block, _ := pem.Decode(dec); block != nil {
			return dec
		}
		// decoding went wrong
		return nil
	}
}

func (bot *TipBot) satdressProxyHandler(ctx intercept.Context) (intercept.Context, error) {
	m := ctx.Message()
	user, err := GetLnbitsUserWithSettings(m.Sender, *bot)
	if err != nil {
		return ctx, err
	}
	if user.Settings == nil || user.Settings.Node.LNDParams == nil {
		bot.trySendMessage(user.Telegram, "You did not register a node yet.")
		log.Errorf("node of user %s not registered", GetUserStr(user.Telegram))
		return ctx, fmt.Errorf("no node settings.")
	}

	var amount int64
	if amount_str, err := getArgumentFromCommand(m.Text, 2); err == nil {
		amount, err = GetAmount(amount_str)
		if err != nil {
			return ctx, err
		}
	}

	memo := "🔀 Payment proxy in."
	invoice, err := bot.createInvoiceWithEvent(ctx, user, amount, memo, "", InvoiceCallbackSatdressProxy, "")
	if err != nil {
		errmsg := fmt.Sprintf("[/invoice] Could not create an invoice: %s", err.Error())
		bot.trySendMessage(user.Telegram, Translate(ctx, "errorTryLaterMessage"))
		log.Errorln(errmsg)
		return ctx, err
	}

	// create qr code
	qr, err := qrcode.Encode(invoice.PaymentRequest, qrcode.Medium, 256)
	if err != nil {
		errmsg := fmt.Sprintf("[/invoice] Failed to create QR code for invoice: %s", err.Error())
		bot.trySendMessage(user.Telegram, Translate(ctx, "errorTryLaterMessage"))
		log.Errorln(errmsg)
		return ctx, err
	}
	bot.trySendMessage(m.Sender, &tb.Photo{File: tb.File{FileReader: bytes.NewReader(qr)}, Caption: fmt.Sprintf("`%s`", invoice.PaymentRequest)})

	log.Infof("[node] Proxy payment for user %s backend %s", GetUserStr(user.Telegram), user.Settings.Node.NodeType)
	return ctx, nil
}

func (bot *TipBot) satdressProxyRelayPaymentHandler(event Event) {
	invoiceEvent := event.(*InvoiceEvent)
	user := invoiceEvent.User
	if user.Settings == nil || user.Settings.Node.LNDParams == nil {
		bot.trySendMessage(user.Telegram, "You did not register a node yet.")
		log.Errorf("node of user %s not registered", GetUserStr(user.Telegram))
		return
	}

	log.Infof("[node:proxy] Relaying payment for user %s backend %s", GetUserStr(user.Telegram), user.Settings.Node.NodeType)

	bot.notifyInvoiceReceivedEvent(invoiceEvent)

	// now relay the payment to the user's node
	var amount int64 = invoiceEvent.Amount

	check_message := bot.trySendMessageEditable(user.Telegram, routingInvoiceMessage)
	var getInvoiceParams satdress.CheckInvoiceParams
	var err error
	if user.Settings.Node.NodeType == "lnd" {
		// get invoice from user's node
		getInvoiceParams, err = satdress.MakeInvoice(
			satdress.Params{
				Backend: satdress.LNDParams{
					Cert:     []byte(user.Settings.Node.LNDParams.CertString),
					Host:     user.Settings.Node.LNDParams.Host,
					Macaroon: user.Settings.Node.LNDParams.Macaroon,
				},
				Msatoshi:    amount * 1000,
				Description: fmt.Sprintf("🔀 Payment proxy out from %s.", GetUserStr(bot.Telegram.Me)),
			},
		)
	} else if user.Settings.Node.NodeType == "lnbits" {
		// get invoice from user's node
		getInvoiceParams, err = satdress.MakeInvoice(
			satdress.Params{
				Backend: satdress.LNBitsParams{
					Key:  user.Settings.Node.LNbitsParams.Key,
					Host: user.Settings.Node.LNbitsParams.Host,
				},
				Msatoshi:    amount * 1000,
				Description: fmt.Sprintf("🔀 Payment proxy out from %s.", GetUserStr(bot.Telegram.Me)),
			},
		)
	}
	if err != nil {
		log.Errorln(err.Error())
		bot.tryEditMessage(check_message, gettingInvoiceErrorMessage)
		return
	}

	// bot.trySendMessage(user.Telegram, fmt.Sprintf("PR: `%s`\n\nHash: `%s`\n\nStatus: `%s`", getInvoiceParams.PR, string(getInvoiceParams.Hash), getInvoiceParams.Status))

	log.Infof("[node:proxy] Retrieved invoice for payment of user %s backend %s. Paying...", GetUserStr(user.Telegram), user.Settings.Node.NodeType)

	// pay invoice
	invoice, err := user.Wallet.Pay(lnbits.PaymentParams{Out: true, Bolt11: getInvoiceParams.PR}, bot.Client)
	if err != nil {
		errmsg := fmt.Sprintf("[/pay] Could not pay invoice of %s: %s", GetUserStr(user.Telegram), err)
		// err = fmt.Errorf(i18n.Translate(payData.LanguageCode, "invoiceUndefinedErrorMessage"))
		// bot.tryEditMessage(c.Message, fmt.Sprintf(i18n.Translate(payData.LanguageCode, "invoicePaymentFailedMessage"), err.Error()), &tb.ReplyMarkup{})
		// verbose error message, turned off for now
		// if len(err.Error()) == 0 {
		// 	err = fmt.Errorf(i18n.Translate(payData.LanguageCode, "invoiceUndefinedErrorMessage"))
		// }
		// bot.tryEditMessage(c.Message, fmt.Sprintf(i18n.Translate(payData.LanguageCode, "invoicePaymentFailedMessage"), str.MarkdownEscape(err.Error())), &tb.ReplyMarkup{})
		log.Errorln(errmsg)
		bot.tryEditMessage(check_message, payingInvoiceErrorMessage)
		return
	}

	// object that holds all information about the send payment
	id := fmt.Sprintf("proxypay:%d:%d:%s", user.Telegram.ID, amount, RandStringRunes(8))

	payData := &PayData{
		Base:    storage.New(storage.ID(id)),
		From:    user,
		Invoice: invoice.PaymentRequest,
		Hash:    invoice.PaymentHash,
		Amount:  amount,
	}
	// add result to persistent struct
	runtime.IgnoreError(payData.Set(payData, bot.Bunt))

	// add the getInvoiceParams to cache to check it later
	bot.Cache.Set(fmt.Sprintf("invoice:%d", user.Telegram.ID), getInvoiceParams, &store.Options{Expiration: 24 * time.Hour})

	time.Sleep(time.Second)

	getInvoiceParams, err = satdress.CheckInvoice(getInvoiceParams)
	if err != nil {
		log.Errorln(err.Error())
		return
	}
	bot.tryEditMessage(check_message, invoiceRoutedMessage)

	log.Infof("[node:proxy] Proxy paid for user %s backend %s.", GetUserStr(user.Telegram), user.Settings.Node.NodeType)
	// bot.trySendMessage(user.Telegram, fmt.Sprintf("PR: `%s`\n\nHash: `%s`\n\nStatus: `%s`", getInvoiceParams.PR, string(getInvoiceParams.Hash), getInvoiceParams.Status))

	return
}
