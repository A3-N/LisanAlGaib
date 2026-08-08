# Managed by LisanAlGaib. Override fish_prompt later in config.fish if desired.
function fish_prompt
    set -l last_status $status
    set -l prompt_user fremen
    if set -q USER; and test -n "$USER"
        set prompt_user $USER
    end

    set_color --bold E5A853
    printf '%s' $prompt_user
    set_color 65513D
    printf '@'
    set_color --bold 88A875
    printf '%s' (prompt_hostname)
    set_color E8D7B4
    printf ' %s' (prompt_pwd)

    if test $last_status -ne 0
        set_color D46A5E
        printf ' [%d]' $last_status
    end

    printf '\n'
    set_color 65513D
    printf '╰─'
    set_color --bold E5A853
    printf '❯ '
    set_color normal
end
