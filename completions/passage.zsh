#compdef passage

_passage() {
  local -a commands common
  commands=(
    'list:List GNU Pass entries'
    'show:Show metadata for one entry'
    'copy:Copy an entry password'
    'reveal:Reveal an entry password'
    'totp:Generate an entry TOTP'
    'mfa:Open MFA-only picker'
    'pin:Pin an entry'
    'unpin:Unpin an entry'
    'clear-recents:Clear MRU timestamps'
    'clear-pins:Clear all pins'
    'clear-clipboard:Clear the clipboard'
    'insert:Create or overwrite an entry'
    'generate:Generate a random password'
    'edit:Edit an entry in $EDITOR'
    'rm:Remove an entry'
    'doctor:Run health checks'
    'access:Report write access per .gpg-id scope'
    'trust:Local-sign recipients to make a scope writable'
    'keys:List local GPG keys'
    'theme:Open the theme builder'
    'version:Print version'
    'help:Show help'
  )
  common=(
    '--store-dir[Pass store directory]:path:_files -/'
    '--state-dir[State directory]:path:_files -/'
    '--json[Emit JSON]'
    '--no-color[Disable color]'
    '--theme-file[Theme file]:path:_files'
    '--no-alt-screen[Disable alternate screen]'
    '--help[Show help]'
  )

  _arguments -C \
    '1:command:->cmds' \
    '*::arg:->args'

  case $state in
    cmds)
      _describe 'command' commands
      ;;
    args)
      case $words[2] in
        list)
          _arguments '--filter[Filter text]:text:' '--mfa[MFA entries only]' $common
          ;;
        copy|reveal)
          _arguments '--private[Do not update recents]' $common
          ;;
        totp)
          _arguments '--no-copy[Do not copy]' '--private[Do not update recents]' '--wait[Wait near expiry]' '--at[Unix timestamp]:timestamp:' $common
          ;;
        mfa)
          _arguments $common
          ;;
        trust)
          _arguments '--full[Also set ownertrust full]' '--import-dir[Import+trust a key folder]:dir:_files -/' '--yes[Skip confirmation]' $common
          ;;
        insert)
          _arguments '--multiline[Read whole stdin]' '--force[Overwrite existing]' $common
          ;;
        generate)
          _arguments '--no-symbols[Alphanumeric only]' '--no-copy[Do not copy]' '--force[Overwrite existing]' $common
          ;;
        rm|remove)
          _arguments '--recursive[Remove a subtree]' '--yes[Skip confirmation]' $common
          ;;
        help)
          _describe 'topic' commands
          ;;
        *)
          _arguments $common
          ;;
      esac
      ;;
  esac
}

_passage "$@"
