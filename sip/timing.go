package sip

import (
	"encoding/json"
	"time"

	"braces.dev/errtrace"
)

// Default values for SIP timers as described in RFC 3261.
const (
	// T1 is the message RTT estimate.
	T1 = 500 * time.Millisecond
	// T2 is the maximum retransmit interval for non-INVITE requests and INVITE responses.
	T2 = 4 * time.Second
	// T4 is the maximum duration a message will remain in the network.
	T4 = 5 * time.Second
	// TimeD is the wait duration for response retransmits via unreliable transport.
	TimeD = 32 * time.Second
	// Time100 is the timeout for automatic 100 Trying response on INVITE.
	Time100 = 200 * time.Millisecond
)

// TimingConfig represents SIP timing config.
// It is used to configure SIP timers as described in RFC 3261.
// Zero value uses default base values [T1], [T2], [T4], [TimeD], [Time100].
// All other timings are calculated based on these base values.
type TimingConfig struct {
	t1, t2, t4,
	timeD,
	time100 time.Duration
}

var defTimingCfg TimingConfig

// NewTimings creates a new SIP timing config with specified base values.
// See [TimingConfig] for more details about how base timing values are used.
func NewTimings(t1, t2, t4, timeD, time100 time.Duration) TimingConfig {
	return TimingConfig{t1, t2, t4, timeD, time100}
}

// T1 is the message RTT estimate.
// It is equal to [T1] if not specified.
func (c TimingConfig) T1() time.Duration {
	if c.t1 == 0 {
		return T1
	}
	return c.t1
}

// T2 is the maximum retransmit interval for non-INVITE requests and INVITE responses.
// It is equal to [T2] if not specified.
func (c TimingConfig) T2() time.Duration {
	if c.t2 == 0 {
		return T2
	}
	return c.t2
}

// T4 is the maximum duration a message will remain in the network.
// It is equal to [T4] if not specified.
func (c TimingConfig) T4() time.Duration {
	if c.t4 == 0 {
		return T4
	}
	return c.t4
}

// Time100 is the timeout for automatic 100 Trying response on INVITE.
// It is equal to [Time100] if not specified.
func (c TimingConfig) Time100() time.Duration {
	if c.time100 == 0 {
		return Time100
	}
	return c.time100
}

// TimeA returns initial INVITE request retransmit interval for unreliable transport.
// It is equal to [TimingConfig.T1].
func (c TimingConfig) TimeA() time.Duration { return c.T1() }

// TimeB returns INVITE client transaction timeout.
// It is equal to 64*[TimingConfig.T1].
func (c TimingConfig) TimeB() time.Duration { return 64 * c.T1() }

// TimeC returns the INVITE transaction timeout on proxy.
// It is equal to 600*[TimingConfig.T1].
func (c TimingConfig) TimeC() time.Duration { return 600 * c.T1() }

// TimeD is the wait duration for response retransmits via unreliable transport.
// It is equal to [TimeD] if not specified.
func (c TimingConfig) TimeD() time.Duration {
	if c.timeD == 0 {
		return TimeD
	}
	return c.timeD
}

// TimeE returns initial non-INVITE request retransmit interval for unreliable transport.
// It is equal to [TimingConfig.T1].
func (c TimingConfig) TimeE() time.Duration { return c.T1() }

// TimeF returns non-INVITE client transaction timeout.
// It is equal to 64*[TimingConfig.T1].
func (c TimingConfig) TimeF() time.Duration { return 64 * c.T1() }

// TimeG returns initial INVITE response retransmit interval for any transport.
// It is equal to [TimingConfig.T1].
func (c TimingConfig) TimeG() time.Duration { return c.T1() }

// TimeH returns timeout for ACK request receipt.
// It is equal to 64*[TimingConfig.T1].
func (c TimingConfig) TimeH() time.Duration { return 64 * c.T1() }

// TimeI returns wait duration for ACK request retransmits via unreliable transport.
// It is equal to [TimingConfig.T4].
func (c TimingConfig) TimeI() time.Duration { return c.T4() }

// TimeJ returns wait duration for non-INVITE request retransmits via unreliable transport.
// It is equal to 64*[TimingConfig.T1].
func (c TimingConfig) TimeJ() time.Duration { return 64 * c.T1() }

// TimeK returns wait duration for response retransmits via unreliable transport.
// It is equal to [TimingConfig.T4].
func (c TimingConfig) TimeK() time.Duration { return c.T4() }

// TimeL returns the wait duration for accepted INVITE request retransmits.
// It is equal to 64*[TimingConfig.T1].
func (c TimingConfig) TimeL() time.Duration { return 64 * c.T1() }

// TimeM returns the wait duration for retransmission of 2xx to INVITE or
// additional 2xx from other branches of a forked INVITE.
// It is equal to 64*[TimingConfig.T1].
func (c TimingConfig) TimeM() time.Duration { return 64 * c.T1() }

func (c TimingConfig) IsZero() bool {
	return c.t1 == 0 && c.t2 == 0 && c.t4 == 0 && c.timeD == 0 && c.time100 == 0
}

type timingConfData struct {
	T1      time.Duration `json:"t1,omitempty"`
	T2      time.Duration `json:"t2,omitempty"`
	T4      time.Duration `json:"t4,omitempty"`
	TimeD   time.Duration `json:"time_d,omitempty"`
	Time100 time.Duration `json:"time_100,omitempty"`
}

func (c TimingConfig) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(json.Marshal(timingConfData{
		T1:      c.t1,
		T2:      c.t2,
		T4:      c.t4,
		TimeD:   c.timeD,
		Time100: c.time100,
	}))
}

func (c *TimingConfig) UnmarshalJSON(data []byte) error {
	var d timingConfData
	if err := json.Unmarshal(data, &d); err != nil {
		return errtrace.Wrap(err)
	}
	c.t1 = d.T1
	c.t2 = d.T2
	c.t4 = d.T4
	c.timeD = d.TimeD
	c.time100 = d.Time100
	return nil
}
