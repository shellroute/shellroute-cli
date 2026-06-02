package tui

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/charmbracelet/lipgloss"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/shellroute/shellroute-cli/internal/session"
)

// Version is set by the cli package at startup.
var Version = "dev"

// Run starts the interactive shell session.
func Run(cfg *config.Config) error {
	printBanner()

	client := api.New(cfg.APIURL, cfg.APIKey)

	// Show balance warning on startup
	if bal, err := client.GetBalance(); err == nil {
		if bal.BalanceUSD == 0 {
			display.Warn("Balance $0.00 — top up to use at https://console.shellroute.com")
		} else if bal.BalanceUSD < 0.50 {
			display.Warn("Balance low (%s) — top up at https://console.shellroute.com", display.FormatBalance(bal.BalanceUSD))
		}
	}

	ctrl := session.NewController(client, cfg)

	ctrlPort, err := ctrl.Start()
	if err != nil {
		return fmt.Errorf("control server: %w", err)
	}

	runShell(ctrlPort, "", cfg.DefaultType)

	// Disconnect if session is still active
	if resp := ctrl.StopAndDisconnect(); resp != nil {
		printSessionSummary(resp)
	}

	return nil
}

// RunWithConnect starts the shell and auto-connects to a country.
func RunWithConnect(cfg *config.Config, country string) error {
	client := api.New(cfg.APIURL, cfg.APIKey)

	if bal, err := client.GetBalance(); err == nil {
		if bal.BalanceUSD == 0 {
			display.Warn("Balance $0.00 — top up to use at https://console.shellroute.com")
		} else if bal.BalanceUSD < 0.50 {
			display.Warn("Balance low (%s) — top up at https://console.shellroute.com", display.FormatBalance(bal.BalanceUSD))
		}
	}

	ctrl := session.NewController(client, cfg)

	ctrlPort, err := ctrl.Start()
	if err != nil {
		return fmt.Errorf("control server: %w", err)
	}

	runShell(ctrlPort, country, cfg.DefaultType)

	if resp := ctrl.StopAndDisconnect(); resp != nil {
		printSessionSummary(resp)
	}

	return nil
}

func printSessionSummary(resp *api.SessionEndResponse) {
	display.SessionSummary("Disconnected.", resp.DurationSec, resp.BytesTotal, resp.CostUSD, resp.BalanceUSD)
}

func printBanner() {
	bold := lipgloss.NewStyle().Bold(true).Render("shellroute")
	ver := lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(8)).Render(" " + Version)
	dim := lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(8))

	fmt.Println()
	fmt.Println("  " + bold + ver)
	fmt.Println()
	fmt.Println(dim.Render("  /connect <country>   Connect and route traffic"))
	fmt.Println(dim.Render("  /countries           List available countries"))
	fmt.Println(dim.Render("  /ssh <user@host>     SSH through the proxy"))
	fmt.Println(dim.Render("  /help                All commands"))
	fmt.Println(dim.Render("  /exit                Quit"))
	fmt.Println()
}

func runShell(ctrlPort int, autoConnect string, defaultType string) {
	// Always use zsh for the shellroute subshell — bash 3.2 (macOS default)
	// lacks tab completion, signal handling, and has session restore noise.
	// We source ~/.bash_profile inside zsh so bash users keep their aliases.
	shell := "/bin/zsh"
	if _, err := os.Stat(shell); err != nil {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
	}

	if defaultType == "" {
		defaultType = "residential"
	}
	env := append(os.Environ(),
		fmt.Sprintf("SHELLROUTE_CTRL=%d", ctrlPort),
		fmt.Sprintf("SHELLROUTE_IPTYPE=%s", defaultType),
		"SHELL_SESSIONS_DISABLE=1",           // suppress macOS session restore on exit
		"BASH_SILENCE_DEPRECATION_WARNING=1", // suppress macOS "default shell is now zsh" nag
	)

	shellName := filepath.Base(shell)
	var args []string

	switch shellName {
	case "bash":
		rcfile := createTempRC("bash", ctrlPort, autoConnect)
		if rcfile != "" {
			args = []string{"--noprofile", "--rcfile", rcfile}
			defer os.Remove(rcfile)
		}
	case "zsh":
		rcfile := createTempRC("zsh", ctrlPort, autoConnect)
		if rcfile != "" {
			env = append(env, "ZDOTDIR="+filepath.Dir(rcfile))
			defer os.RemoveAll(filepath.Dir(rcfile))
		}
	}

	cmd := exec.Command(shell, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env

	signal.Ignore(syscall.SIGINT)
	cmd.Run()
}

func createTempRC(shellType string, ctrlPort int, autoConnect string) string {
	switch shellType {
	case "bash":
		f, err := os.CreateTemp("", "shellroute-rc-*.sh")
		if err != nil {
			return ""
		}
		home := os.Getenv("HOME")
		// Disable macOS session save/restore
		fmt.Fprintln(f, "export SHELL_SESSIONS_DISABLE=1")
		fmt.Fprintln(f, "shell_session_save() { :; }")
		fmt.Fprintln(f, "shell_session_update() { :; }")
		fmt.Fprintln(f, "shell_session_history() { :; }")
		fmt.Fprintln(f, "shell_session_delete() { :; }")
		// Source user's startup files (skip /etc/profile — loads macOS session crud)
		fmt.Fprintf(f, "if [ -f %s/.bash_profile ]; then source %s/.bash_profile\n", home, home)
		fmt.Fprintf(f, "elif [ -f %s/.bash_login ]; then source %s/.bash_login\n", home, home)
		fmt.Fprintf(f, "elif [ -f %s/.profile ]; then source %s/.profile; fi\n", home, home)
		fmt.Fprintf(f, "[ -f %s/.bashrc ] && source %s/.bashrc\n", home, home)
		// Re-stub after sourcing — user files may re-enable session management
		fmt.Fprintln(f, "shell_session_save() { :; }")
		fmt.Fprintln(f, "shell_session_update() { :; }")
		writeBashPrompt(f)
		writeShellFunctions(f)
		if autoConnect != "" {
			fmt.Fprintf(f, "/connect %s\n", autoConnect)
		}
		f.Close()
		return f.Name()

	case "zsh":
		dir, err := os.MkdirTemp("", "shellroute-zsh-*")
		if err != nil {
			return ""
		}
		home := os.Getenv("HOME")

		// Custom ZDOTDIR overrides ALL zsh startup files — we must source
		// the user's originals so they get their aliases, PATH, functions.
		// Order mirrors a login+interactive zsh: zshenv → zprofile → zshrc → zlogin

		// .zshenv — always runs (env vars, PATH)
		zshenv := filepath.Join(dir, ".zshenv")
		if ef, err := os.Create(zshenv); err == nil {
			fmt.Fprintf(ef, "[ -f %s/.zshenv ] && source %s/.zshenv\n", home, home)
			ef.Close()
		}

		// .zshrc — interactive shell config + our additions
		rc := filepath.Join(dir, ".zshrc")
		f, err := os.Create(rc)
		if err != nil {
			return ""
		}
		fmt.Fprintf(f, "[ -f %s/.zprofile ] && source %s/.zprofile\n", home, home)
		fmt.Fprintf(f, "[ -f %s/.zshrc ] && source %s/.zshrc\n", home, home)
		fmt.Fprintf(f, "[ -f %s/.zlogin ] && source %s/.zlogin\n", home, home)
		// Source bash config too — many macOS users have aliases/PATH in .bash_profile
		fmt.Fprintf(f, "[ -f %s/.bash_profile ] && source %s/.bash_profile 2>/dev/null\n", home, home)
		writeZshPrompt(f)
		writeShellFunctions(f)
		if autoConnect != "" {
			fmt.Fprintf(f, "/connect %s\n", autoConnect)
		}
		f.Close()
		return rc
	}

	return ""
}

func writeBashPrompt(f *os.File) {
	// Dynamic prompt via PROMPT_COMMAND — updates on every command
	// Also checks health: if connection is dead, activates kill switch (unsets proxy)
	fmt.Fprint(f, `_sr_route_ok=1
_sr_prompt() {
  local _sr_msg=""

  if [ -n "$SHELLROUTE_SESSION_ID" ]; then
    local _h
    _h=$(curl -sf --max-time 3 "http://127.0.0.1:${SHELLROUTE_CTRL}/route-check" 2>/dev/null)

    if echo "$_h" | grep -q '^ok'; then
      _sr_route_ok=1
      local _new_ip=$(echo "$_h" | cut -d' ' -f2)
      if [ -n "$_new_ip" ] && [ "$_new_ip" != "$SHELLROUTE_EXIT_IP" ]; then
        export SHELLROUTE_EXIT_IP="$_new_ip"
      fi
      if [ -n "$_SR_KILLSWITCH_SHOWN" ]; then
        _sr_msg="  \033[32m✓ Connection restored — IP: ${SHELLROUTE_EXIT_IP}\033[0m"
        _SR_KILLSWITCH_SHOWN=""
      fi
    else
      # Any non-ok response: connection is down (depleted, dead, empty, unknown)
      _sr_route_ok=0
      local _reason="lost"
      echo "$_h" | grep -q '^dead depleted' && _reason="depleted"
      if [ "$_reason" = "depleted" ]; then
        if [ "$_SR_KILLSWITCH_SHOWN" != "depleted" ]; then
          _sr_msg="  \033[33m✗ Balance depleted. Top up, then run /connect to reconnect.\033[0m"
          _SR_KILLSWITCH_SHOWN="depleted"
        fi
      else
        # Auto-reconnect on connection lost
        _sr_msg="  \033[33m⟳ Reconnecting...\033[0m"
        _SR_KILLSWITCH_SHOWN="reconnecting"
        echo -e "$_sr_msg"
        _sr_msg=""
        local _rr
        _rr=$(curl -sf --max-time 30 -X POST \
          "http://127.0.0.1:${SHELLROUTE_CTRL}/rotate-and-wait" 2>/dev/null)
        if echo "$_rr" | grep -q '^ok'; then
          local _new_ip=$(echo "$_rr" | cut -d' ' -f2)
          [ -n "$_new_ip" ] && export SHELLROUTE_EXIT_IP="$_new_ip"
          _sr_msg="  \033[32m✓ Reconnected — IP: ${SHELLROUTE_EXIT_IP}\033[0m"
          _SR_KILLSWITCH_SHOWN=""
          _sr_route_ok=1
        else
          _sr_msg="  \033[31m✗ Reconnect failed. Run /connect.\033[0m"
          _SR_KILLSWITCH_SHOWN="lost"
        fi
      fi
    fi
  else
    _sr_route_ok=1
  fi

  # --- Print message (if any), then ALWAYS set prompt ---
  [ -n "$_sr_msg" ] && echo -e "$_sr_msg"

  if [ "$_SR_KILLSWITCH_SHOWN" = "depleted" ]; then
    PS1="\[\033[33m\]●\[\033[0m\]\[\033[1m\] shellroute\[\033[0m\] · \[\033[33m\]balance depleted\[\033[0m\]\n\[\033[90m\]\w\[\033[0m\] \[\033[33m\]❯\[\033[0m\] "
  elif [ "$_SR_KILLSWITCH_SHOWN" = "lost" ]; then
    PS1="\[\033[31m\]●\[\033[0m\]\[\033[1m\] shellroute\[\033[0m\] · \[\033[31m\]connection lost\[\033[0m\]\n\[\033[90m\]\w\[\033[0m\] \[\033[31m\]❯\[\033[0m\] "
  elif [ -n "$SHELLROUTE_COUNTRY" ]; then
    local cc=$(echo "$SHELLROUTE_COUNTRY" | tr '[:upper:]' '[:lower:]')
    local ip="$SHELLROUTE_EXIT_IP"
    PS1="\[\033[32m\]●\[\033[0m\]\[\033[1m\] shellroute\[\033[0m\] \[\033[90m\]${cc} · ${ip}\[\033[0m\]\n\[\033[90m\]\w\[\033[0m\] \[\033[32m\]❯\[\033[0m\] "
  else
    PS1="\[\033[32m\]●\[\033[0m\]\[\033[1m\] shellroute\[\033[0m\] · not connected · direct traffic\n\[\033[90m\]\w\[\033[0m\] \[\033[32m\]❯\[\033[0m\] "
  fi
}
PROMPT_COMMAND=_sr_prompt

# Connection guard: DEBUG trap blocks commands when route is dead.
# Only reads a shell variable — zero overhead. Route check happens in PROMPT_COMMAND above.
shopt -s extdebug
trap '_sr_debug_guard' DEBUG
_sr_debug_guard() {
  [ "$_SR_GUARD_DISABLED" = "1" ] && return 0
  [[ "$BASH_COMMAND" = _sr_* ]] && return 0
  case "$BASH_COMMAND" in
    /connect*|/disconnect*|/rotate*|/status*|/countries*|/cities*|\
    /ssh*|/help*|/exit*|/balance*|/iptype*|/sticky*|/login*|/logout*)
      return 0 ;;
  esac
  [ "$_sr_route_ok" = "1" ] && return 0
  return 1
}
`)
	// Ctrl+C clears line (default bash behavior). Exit with /exit or exit.
}

func writeZshPrompt(f *os.File) {
	// Dynamic prompt via precmd — updates on every command
	// Written with WriteString to avoid go vet false positive on zsh %F sequences
	f.WriteString(`setopt PROMPT_SUBST
unsetopt SHARE_HISTORY 2>/dev/null

_sr_prompt() {
  # Route check in precmd — same as bash PROMPT_COMMAND.
  # accept-line checks BEFORE command; precmd checks AFTER command.
  if [[ -n "$SHELLROUTE_SESSION_ID" ]]; then
    local _h
    _h=$(curl -sf --max-time 3 "http://127.0.0.1:${SHELLROUTE_CTRL}/route-check" 2>/dev/null)
    if [[ "$_h" = ok* ]]; then
      local _new_ip=${_h#ok }
      [[ -n "$_new_ip" && "$_new_ip" != "$SHELLROUTE_EXIT_IP" ]] && export SHELLROUTE_EXIT_IP="$_new_ip"
      if [[ -n "$_SR_KILLSWITCH_SHOWN" ]]; then
        print $'\e[32m  ✓ Connection restored — IP: '"${SHELLROUTE_EXIT_IP}"$'\e[0m'
        _SR_KILLSWITCH_SHOWN=""
      fi
    else
      local _reason="lost"
      [[ "$_h" = "dead depleted"* ]] && _reason="depleted"
      if [[ "$_SR_KILLSWITCH_SHOWN" != "$_reason" ]]; then
        if [[ "$_reason" = "depleted" ]]; then
          print $'\e[33m  ✗ Balance depleted. Top up, then run /connect to reconnect.\e[0m'
        else
          print $'\e[31m  ✗ Connection lost. Run /connect.\e[0m'
        fi
        _SR_KILLSWITCH_SHOWN="$_reason"
      fi
    fi
  fi

  # Set prompt based on state
  if [[ "$_SR_KILLSWITCH_SHOWN" = "depleted" ]]; then
    PROMPT=$'%F{yellow}●%f %Bshellroute%b · %F{yellow}balance depleted%f\n%F{242}%~%f %F{yellow}❯%f '
  elif [[ "$_SR_KILLSWITCH_SHOWN" = "lost" ]]; then
    PROMPT=$'%F{red}●%f %Bshellroute%b · %F{red}connection lost%f\n%F{242}%~%f %F{red}❯%f '
  elif [[ -n "$SHELLROUTE_COUNTRY" ]]; then
    PROMPT=$'%F{green}●%f %Bshellroute%b %F{242}${(L)SHELLROUTE_COUNTRY} · ${SHELLROUTE_EXIT_IP}%f\n%F{242}%~%f %F{green}❯%f '
  else
    PROMPT=$'%F{green}●%f %Bshellroute%b · not connected · direct traffic\n%F{242}%~%f %F{green}❯%f '
  fi
}
precmd_functions+=(_sr_prompt)

# Connection guard: check route health before accepting Enter.
# If dead, block and reconnect. If depleted, show message.
_sr_accept_line() {
  if [[ -z "$SHELLROUTE_SESSION_ID" ]]; then
    zle .accept-line
    return
  fi
  # Skip shellroute slash commands
  local trimmed="${${BUFFER}##[[:space:]]#}"
  case "$trimmed" in
    /connect*|/disconnect*|/rotate*|/status*|/countries*|/cities*|\
    /ssh*|/help*|/exit*|/balance*|/iptype*|/sticky*|/login*|/logout*)
      zle .accept-line
      return
      ;;
  esac

  local health
  health=$(curl -sf --max-time 3 \
    "http://127.0.0.1:${SHELLROUTE_CTRL}/route-check" 2>/dev/null)

  if [[ "$health" = ok* ]]; then
    _SR_HEALTH="$health"
    local ip=${health#ok }
    if [[ -n "$ip" && "$ip" != "$SHELLROUTE_EXIT_IP" ]]; then
      export SHELLROUTE_EXIT_IP="$ip"
    fi
    if [[ -n "$_SR_KILLSWITCH_SHOWN" ]]; then
      _SR_KILLSWITCH_SHOWN=""
      zle -M "  ✓ Connection restored — IP: ${SHELLROUTE_EXIT_IP}"
    fi
    zle .accept-line
    return
  fi

  # Any non-ok: connection is down (depleted, dead, empty, unknown)
  local _reason="lost"
  [[ "$health" = "dead depleted"* ]] && _reason="depleted"

  if [[ "$_reason" = "depleted" ]]; then
    if [[ "$_SR_KILLSWITCH_SHOWN" != "depleted" ]]; then
      _SR_KILLSWITCH_SHOWN="depleted"
      zle -M "  ✗ Balance depleted. Top up, then run /connect to reconnect."
      _SR_HEALTH="dead"
      zle reset-prompt
    else
      # Already shown — let command through
      zle .accept-line
    fi
    return
  fi

  # Auto-reconnect on connection lost
  zle -M "  ⟳ Reconnecting..."
  zle reset-prompt
  local _rr
  _rr=$(curl -sf --max-time 30 -X POST \
    "http://127.0.0.1:${SHELLROUTE_CTRL}/rotate-and-wait" 2>/dev/null)
  if [[ "$_rr" = ok* ]]; then
    local _new_ip=${_rr#ok }
    [[ -n "$_new_ip" ]] && export SHELLROUTE_EXIT_IP="$_new_ip"
    _SR_KILLSWITCH_SHOWN=""
    _SR_HEALTH="ok"
    zle -M "  ✓ Reconnected — IP: ${SHELLROUTE_EXIT_IP}"
    zle reset-prompt
    zle .accept-line
  else
    _SR_KILLSWITCH_SHOWN="lost"
    _SR_HEALTH="dead"
    zle -M "  ✗ Reconnect failed. Run /connect."
    zle reset-prompt
  fi
}
zle -N accept-line _sr_accept_line

# Ctrl+C — double press to exit
_sr_ctrlc=0
_sr_at_prompt=0
preexec_functions+=(_sr_preexec)
_sr_preexec() { _sr_at_prompt=0; _sr_ctrlc=0; }
precmd_functions+=(_sr_precmd_int)
_sr_precmd_int() { _sr_at_prompt=1; }
TRAPINT() {
  if (( ! _sr_at_prompt )); then return $(( 128 + $1 )); fi
  _sr_ctrlc=$((_sr_ctrlc + 1))
  if (( _sr_ctrlc >= 2 )); then builtin exit; fi
  print "\n  Press Ctrl+C again to exit."
  { sleep 2 && _sr_ctrlc=0 } &>/dev/null &!
  return $(( 128 + $1 ))
}

# Tab completion for slash commands
autoload -Uz compinit && compinit -C 2>/dev/null
_sr_complete() {
  local -a cmds
  cmds=('/connect:Connect and route traffic' '/disconnect:Disconnect session' '/rotate:Rotate IP' '/status:Session info' '/countries:List countries' '/ssh:SSH through proxy' '/help:Show help' '/exit:Quit')
  _describe 'shellroute' cmds
}
compdef _sr_complete -first-
`)
}
