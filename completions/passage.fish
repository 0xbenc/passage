complete -c passage -f
complete -c passage -n "__fish_use_subcommand" -a "list" -d "List GNU Pass entries"
complete -c passage -n "__fish_use_subcommand" -a "show" -d "Show metadata"
complete -c passage -n "__fish_use_subcommand" -a "copy" -d "Copy password"
complete -c passage -n "__fish_use_subcommand" -a "reveal" -d "Reveal password"
complete -c passage -n "__fish_use_subcommand" -a "totp" -d "Generate TOTP"
complete -c passage -n "__fish_use_subcommand" -a "mfa" -d "Open MFA-only picker"
complete -c passage -n "__fish_use_subcommand" -a "pin" -d "Pin entry"
complete -c passage -n "__fish_use_subcommand" -a "unpin" -d "Unpin entry"
complete -c passage -n "__fish_use_subcommand" -a "clear-recents" -d "Clear recents"
complete -c passage -n "__fish_use_subcommand" -a "clear-pins" -d "Clear pins"
complete -c passage -n "__fish_use_subcommand" -a "clear-clipboard" -d "Clear clipboard"
complete -c passage -n "__fish_use_subcommand" -a "doctor" -d "Run health checks"
complete -c passage -n "__fish_use_subcommand" -a "keys" -d "List GPG keys"
complete -c passage -n "__fish_use_subcommand" -a "version" -d "Print version"
complete -c passage -n "__fish_use_subcommand" -a "help" -d "Show help"
complete -c passage -l store-dir -d "Pass store directory" -r
complete -c passage -l state-dir -d "State directory" -r
complete -c passage -l json -d "Emit JSON"
complete -c passage -l filter -d "Filter entries" -r
complete -c passage -l mfa -d "MFA entries only"
complete -c passage -l private -d "Do not update recents"
complete -c passage -l no-copy -d "Do not copy TOTP"
complete -c passage -l wait -d "Wait for next TOTP near expiry"
complete -c passage -l at -d "Unix timestamp for TOTP" -r
complete -c passage -l no-color -d "Disable color"
complete -c passage -l theme-file -d "Theme file" -r
complete -c passage -l no-alt-screen -d "Disable alternate screen"
complete -c passage -l help -d "Show help"
