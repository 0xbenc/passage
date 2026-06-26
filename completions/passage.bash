# bash completion for passage

_passage()
{
    local cur prev words cword
    _init_completion || return

    local commands="list show copy reveal totp mfa pin unpin clear-recents clear-pins clear-clipboard doctor access keys theme version help"
    local common_flags="--store-dir --state-dir --json --no-color --theme-file --no-alt-screen --help"

    if [[ $cword -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "$commands $common_flags" -- "$cur") )
        return
    fi

    case "${words[1]}" in
        list)
            COMPREPLY=( $(compgen -W "--json --filter --mfa --store-dir --state-dir --no-color --theme-file --help" -- "$cur") )
            return
            ;;
        show)
            COMPREPLY=( $(compgen -W "--json --store-dir --state-dir --help" -- "$cur") )
            return
            ;;
        copy)
            COMPREPLY=( $(compgen -W "--private --store-dir --state-dir --help" -- "$cur") )
            return
            ;;
        reveal)
            COMPREPLY=( $(compgen -W "--private --store-dir --state-dir --no-color --theme-file --no-alt-screen --help" -- "$cur") )
            return
            ;;
        totp)
            COMPREPLY=( $(compgen -W "--json --no-copy --private --wait --at --store-dir --state-dir --no-color --theme-file --no-alt-screen --help" -- "$cur") )
            return
            ;;
        mfa)
            COMPREPLY=( $(compgen -W "--store-dir --state-dir --no-color --theme-file --no-alt-screen --help" -- "$cur") )
            return
            ;;
        pin|unpin)
            COMPREPLY=( $(compgen -W "--store-dir --state-dir --help" -- "$cur") )
            return
            ;;
        clear-recents|clear-pins)
            COMPREPLY=( $(compgen -W "--state-dir --help" -- "$cur") )
            return
            ;;
        doctor|keys)
            COMPREPLY=( $(compgen -W "--json --store-dir --no-color --theme-file --no-alt-screen --help" -- "$cur") )
            return
            ;;
        access)
            COMPREPLY=( $(compgen -W "--json --store-dir --help" -- "$cur") )
            return
            ;;
        theme)
            COMPREPLY=( $(compgen -W "export import --theme-file --no-color --no-alt-screen --help" -- "$cur") )
            return
            ;;
        help)
            COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
            return
            ;;
        *)
            COMPREPLY=( $(compgen -W "$common_flags" -- "$cur") )
            return
            ;;
    esac
}

complete -F _passage passage
