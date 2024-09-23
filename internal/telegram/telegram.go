package telegram

import (
	"fmt"
	"strconv"
	"time"

	"github.com/massmux/SatsMobiBot/internal/rate"
	"github.com/eko/gocache/store"
	log "github.com/sirupsen/logrus"
	tb "gopkg.in/lightningtipbot/telebot.v3"
)

// getChatIdFromRecipient will parse the recipient to int64
func (bot *TipBot) getChatIdFromRecipient(to tb.Recipient) (int64, error) {
	chatId, err := strconv.ParseInt(to.Recipient(), 10, 64)
	if err != nil {
		return 0, err
	}
	return chatId, nil
}

func (bot TipBot) tryForwardMessage(to tb.Recipient, what tb.Editable, options ...interface{}) (msg *tb.Message) {
	rate.CheckLimit(to)
	// ChatId is used for the keyboard
	chatId, err := bot.getChatIdFromRecipient(to)
	if err != nil {
		log.Errorf("[tryForwardMessage] error converting message recipient to int64: %v", err)
		return
	}
	msg, err = bot.Telegram.Forward(to, what, bot.appendMainMenu(chatId, to, options)...)
	if err != nil {
		log.Warnln(err.Error())
	}
	return
}
func (bot TipBot) trySendMessage(to tb.Recipient, what interface{}, options ...interface{}) (msg *tb.Message) {
	rate.CheckLimit(to)
	// ChatId is used for the keyboard
	chatId, err := bot.getChatIdFromRecipient(to)
	if err != nil {
		log.Errorf("[trySendMessage] error converting message recipient to int64: %v", err)
		return
	}
	log.Tracef("[trySendMessage] chatId: %d", chatId)
	msg, err = bot.Telegram.Send(to, what, bot.appendMainMenu(chatId, to, options)...)
	if err != nil {
		log.Warnln(err.Error())
	}
	return
}

func (bot TipBot) trySendMessageEditable(to tb.Recipient, what interface{}, options ...interface{}) (msg *tb.Message) {
	rate.CheckLimit(to)
	msg, err := bot.Telegram.Send(to, what, options...)
	if err != nil {
		log.Warnln(err.Error())
	}
	return
}

func (bot TipBot) tryReplyMessage(to *tb.Message, what interface{}, options ...interface{}) (msg *tb.Message) {
	rate.CheckLimit(to)
	msg, err := bot.Telegram.Reply(to, what, bot.appendMainMenu(to.Chat.ID, to, options)...)
	if err != nil {
		log.Warnln(err.Error())
	}
	return
}

func (bot TipBot) tryEditMessage(to tb.Editable, what interface{}, options ...interface{}) (msg *tb.Message, err error) {
	// get a sig for the rate limiter
	sig, chat := to.MessageSig()
	if chat != 0 {
		sig = strconv.FormatInt(chat, 10)
	}
	rate.CheckLimit(sig)

	_, chatId := to.MessageSig()
	log.Tracef("[tryEditMessage] sig: %s, chatId: %d", sig, chatId)
	msg, err = bot.Telegram.Edit(to, what, options...)
	if err != nil {
		log.Warnln(err.Error())
	}
	return
}

func (bot TipBot) tryDeleteMessage(msg tb.Editable) {
	if !allowedToPerformAction(bot, msg, isAdminAndCanDelete) {
		return
	}
	rate.CheckLimit(msg)
	err := bot.Telegram.Delete(msg)
	if err != nil {
		log.Warnln(err.Error())
	}
	return

}

// allowedToPerformAction will check if bot is allowed to perform an action on the tb.Editable.
// this function will persist the admins list in cache for 5 minutes.
// if no admins list is found for this group, bot will always fetch a fresh list.
func allowedToPerformAction(bot TipBot, editable tb.Editable, action func(members []tb.ChatMember, me *tb.User) bool) bool {
	switch editable.(type) {
	case *tb.Message:
		message := editable.(*tb.Message)
		if message.Sender.ID == bot.Telegram.Me.ID {
			break
		}
		chat := message.Chat
		if chat.Type == tb.ChatSuperGroup || chat.Type == tb.ChatGroup {
			admins, err := bot.Cache.Get(fmt.Sprintf("admins-%d", chat.ID))
			if err != nil {
				admins, err = bot.Telegram.AdminsOf(message.Chat)
				if err != nil {
					log.Warnln(err.Error())
					return false
				}
				bot.Cache.Set(fmt.Sprintf("admins-%d", chat.ID), admins, &store.Options{Expiration: 5 * time.Minute})
			}
			if action(admins.([]tb.ChatMember), bot.Telegram.Me) {
				return true
			}
			return false
		}
	}
	return true
}

// isAdminAndCanDelete will check if me is in members list and allowed to delete messages
func isAdminAndCanDelete(members []tb.ChatMember, me *tb.User) bool {
	for _, admin := range members {
		if admin.User.ID == me.ID {
			return admin.CanDeleteMessages
		}
	}
	return false
}

// isOwner will check if user is owner of group
func (bot *TipBot) isOwner(chat *tb.Chat, me *tb.User) bool {
	members, err := bot.Telegram.AdminsOf(chat)
	if err != nil {
		log.Warnln(err.Error())
		return false
	}
	for _, admin := range members {
		if admin.User.ID == me.ID && admin.Role == "creator" {
			return true
		}
	}
	return false
}

// isAdmin will check if user is admin in a group
func (bot *TipBot) isAdmin(chat *tb.Chat, me *tb.User) bool {
	members, err := bot.Telegram.AdminsOf(chat)
	if err != nil {
		log.Warnln(err.Error())
		return false
	}
	for _, admin := range members {
		if admin.User.ID == me.ID {
			return true
		}
	}
	return false
}

// isAdmin will check if user is admin in a group
func (bot *TipBot) isAdminAndCanInviteUsers(chat *tb.Chat, me *tb.User) bool {
	members, err := bot.Telegram.AdminsOf(chat)
	if err != nil {
		log.Warnln(err.Error())
		return false
	}
	for _, admin := range members {
		if admin.User.ID == me.ID {
			return admin.CanInviteUsers
		}
	}
	return false
}
