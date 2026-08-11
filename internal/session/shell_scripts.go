package session

// NoProxyUnionScript is the shell function that unions NO_PROXY + no_proxy
// + loopback entries into a single deduplicated list. Both variables receive
// the identical result. Glob-safe (no unquoted expansion of *) and portable
// across bash and zsh.
const NoProxyUnionScript = `_sr_union_no_proxy() {
  local seen="" result="" h
  local combined="${NO_PROXY:+$NO_PROXY,}${no_proxy}"
  while [ -n "$combined" ]; do
    h="${combined%%,*}"
    if [ "$h" = "$combined" ]; then combined=""; else combined="${combined#*,}"; fi
    h="${h## }"; h="${h%% }"
    [ -z "$h" ] && continue
    case ",$seen," in *",$h,"*) continue ;; esac
    seen="$seen,$h"; result="${result:+$result,}$h"
  done
  for h in localhost 127.0.0.1 ::1; do
    case ",$seen," in *",$h,"*) continue ;; esac
    result="${result:+$result,}$h"
  done
  printf '%s' "$result"
}`

// NodeProxyOwnershipScript is the shell conditional that sets
// NODE_USE_ENV_PROXY=1 with an ownership marker if the user hasn't set it.
const NodeProxyOwnershipScript = `if [ -z "$NODE_USE_ENV_PROXY" ]; then export NODE_USE_ENV_PROXY=1; _SR_OWNS_NODE_PROXY=1; fi`
