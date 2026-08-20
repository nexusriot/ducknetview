# bash completion for ducknetview
_ducknetview() {
    local cur prev opts tabs
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    opts="--once --json --from --metrics --top --usage --usage-all
          --check-baseline --baseline --save-baseline --log-events
          --on-alert --tab --version --help"
    tabs="overview interfaces ports processes procs connections conns events routes neighbours"

    case "$prev" in
        --tab)
            COMPREPLY=( $(compgen -W "$tabs" -- "$cur") )
            return 0
            ;;
        --from|--baseline|--save-baseline|--log-events|--on-alert)
            COMPREPLY=( $(compgen -f -- "$cur") )
            return 0
            ;;
        --top|--usage)
            return 0
            ;;
    esac

    COMPREPLY=( $(compgen -W "$opts" -- "$cur") )
}
complete -F _ducknetview ducknetview
