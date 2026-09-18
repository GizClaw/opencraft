package execd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/GizClaw/flowcraft/core/sandbox"
)

// execOptionsToProto renders a core ExecOptions for the wire. The net
// policy stays JSON: it is a large struct owned by core, and mirroring
// it in protobuf would couple this schema to every core release.
func execOptionsToProto(opts sandbox.ExecOptions) (*ExecOptions, error) {
	out := &ExecOptions{
		WorkDir:        opts.WorkDir,
		Stdin:          opts.Stdin,
		TimeoutMs:      opts.Timeout.Milliseconds(),
		EnvAllow:       opts.Env.Allow,
		EnvInject:      opts.Env.Inject,
		EnvAllowSet:    opts.Env.Allow != nil,
		Write:          uint32(opts.Write),
		MemoryBytes:    opts.Resources.MemoryBytes,
		CpuMillicores:  int64(opts.Resources.CPUMillicores),
		DiskBytes:      opts.Resources.DiskBytes,
		MaxOutputBytes: opts.Resources.MaxOutputBytes,
	}
	raw, err := json.Marshal(opts.Net)
	if err != nil {
		return nil, fmt.Errorf("execd: encode net policy: %w", err)
	}
	out.NetPolicyJson = raw
	return out, nil
}

func execOptionsFromProto(in *ExecOptions) (sandbox.ExecOptions, error) {
	if in == nil {
		return sandbox.ExecOptions{}, nil
	}
	opts := sandbox.ExecOptions{
		WorkDir: in.GetWorkDir(),
		Stdin:   in.GetStdin(),
		Timeout: time.Duration(in.GetTimeoutMs()) * time.Millisecond,
		Write:   sandbox.WritePolicy(in.GetWrite()),
		Resources: sandbox.ResourceLimits{
			MemoryBytes:    in.GetMemoryBytes(),
			CPUMillicores:  int(in.GetCpuMillicores()),
			DiskBytes:      in.GetDiskBytes(),
			MaxOutputBytes: in.GetMaxOutputBytes(),
		},
	}
	if in.GetEnvAllowSet() {
		opts.Env.Allow = in.GetEnvAllow()
		if opts.Env.Allow == nil {
			opts.Env.Allow = []string{}
		}
	}
	if inject := in.GetEnvInject(); len(inject) > 0 {
		opts.Env.Inject = inject
	}
	if raw := in.GetNetPolicyJson(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &opts.Net); err != nil {
			return sandbox.ExecOptions{}, fmt.Errorf("execd: decode net policy: %w", err)
		}
	}
	return opts, nil
}

func streamToProto(stream sandbox.SessionStream) uint32 {
	return uint32(stream)
}

func streamFromProto(value uint32) sandbox.SessionStream {
	switch sandbox.SessionStream(value) {
	case sandbox.SessionStreamStderr:
		return sandbox.SessionStreamStderr
	case sandbox.SessionStreamTTY:
		return sandbox.SessionStreamTTY
	default:
		return sandbox.SessionStreamStdout
	}
}

func exitReasonToProto(reason sandbox.SessionExitReason) uint32 {
	return uint32(reason)
}

func exitReasonFromProto(value uint32) sandbox.SessionExitReason {
	switch sandbox.SessionExitReason(value) {
	case sandbox.SessionSignaled:
		return sandbox.SessionSignaled
	case sandbox.SessionTerminated:
		return sandbox.SessionTerminated
	default:
		return sandbox.SessionExited
	}
}

// signalInterrupt is the only session signal the protocol carries.
const signalInterrupt uint32 = 0

func signalToCore(value uint32) (sandbox.SessionSignal, bool) {
	if value != signalInterrupt {
		return 0, false
	}
	return sandbox.SessionSignalInterrupt, true
}
