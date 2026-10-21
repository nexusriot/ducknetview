# bash completion for ducknetview
_ducknetview() {
    local cur prev opts tabs
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    opts="--once --json --from --diff --metrics --metrics-listen --top
          --usage --usage-all --daemon --check-baseline --check-config
          --baseline --save-baseline --log-events --on-alert --tab --view
          --version --help"
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
        --view)
            # An explicitly empty --view lists the saved views and exits.
            COMPREPLY=( $(compgen -W "$(ducknetview --view= 2>/dev/null)" -- "$cur") )
            return 0
            ;;
        --top|--usage|--metrics-listen)
            return 0
            ;;
    esac

    # --diff takes two snapshot files as positional arguments.
    for word in "${COMP_WORDS[@]}"; do
        if [ "$word" = "--diff" ]; then
            COMPREPLY=( $(compgen -f -- "$cur") )
            return 0
        fi
    done

    COMPREPLY=( $(compgen -W "$opts" -- "$cur") )
}
complete -F _ducknetview ducknetview
