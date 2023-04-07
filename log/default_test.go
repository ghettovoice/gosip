package log_test

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gbytes"

	"github.com/ghettovoice/gosip/log"
)

var _ = Describe("Log", Label("log"), func() {
	Describe("DefaultLogger", func() {
		var (
			b   *Buffer
			l   *log.DefaultLogger
			lvl log.Level
		)

		JustBeforeEach(func() {
			b = NewBuffer()
			l = log.NewDefaultLogger("sip", lvl, b)
		})

		When("level = disabled", func() {
			BeforeEach(func() {
				lvl = log.LevelDisabled
			})

			It("should not log messages", func() {
				l.Debug("qwerty", nil)
				Expect(b).NotTo(Say(`.+`))
			})
		})

		When("level = debug", func() {
			BeforeEach(func() {
				lvl = log.LevelDebug
			})

			It("should log debug messages", func() {
				l.Debug("qwerty", map[string]any{
					"proto":      "UDP",
					"local_addr": "127.0.0.1:5060",
					"error":      fmt.Sprint(errors.New("failure")),
					"~dump":      []byte("abc\n\tqwe\n\tzxc"),
				})

				Expect(b).To(Say(`sip DEBUG | \d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}.\d{6} .+: qwerty error="failure" local_addr="127.0.0.1:5060" proto="UDP"`))
				Expect(b).To(Say(`abc\n\tqwe\n\tzxc`))
			})

			It("should log warning messages", func() {
				l.Warn("qwerty", map[string]any{
					"proto":      "UDP",
					"local_addr": "127.0.0.1:5060",
				})

				Expect(b).To(Say(`sip WARNING | \d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}.\d{6} .+: qwerty local_addr="127.0.0.1:5060" proto="UDP"`))
			})
		})

		When("level = warn", func() {
			BeforeEach(func() {
				lvl = log.LevelWarn
			})

			It("should log warning messages", func() {
				l.Warn("qwerty", map[string]any{
					"proto":      "UDP",
					"local_addr": "127.0.0.1:5060",
				})

				Expect(b).To(Say(`sip WARNING | \d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}.\d{6} .+: qwerty local_addr="127.0.0.1:5060" proto="UDP"`))
			})

			It("should not log debug messages", func() {
				l.Debug("qwerty", map[string]any{
					"proto":      "UDP",
					"local_addr": "127.0.0.1:5060",
				})

				Expect(b).NotTo(Say(`.+`))
			})
		})

		When("level = error", func() {
			BeforeEach(func() {
				lvl = log.LevelError
			})

			It("should log error messages", func() {
				l.Error("qwerty", map[string]any{
					"proto":      "UDP",
					"local_addr": "127.0.0.1:5060",
				})

				Expect(b).To(Say(`sip ERROR | \d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}.\d{6} .+: qwerty local_addr="127.0.0.1:5060" proto="UDP"`))
			})

			It("should not log debug messages", func() {
				l.Debug("qwerty", map[string]any{
					"proto":      "UDP",
					"local_addr": "127.0.0.1:5060",
				})

				Expect(b).NotTo(Say(`.+`))
			})

			It("should not log warning messages", func() {
				l.Warn("qwerty", map[string]any{
					"proto":      "UDP",
					"local_addr": "127.0.0.1:5060",
				})

				Expect(b).NotTo(Say(`.+`))
			})
		})
	})
})
