- A user name is accepted before the host of a `<vcs>+ssh://` location —
  `git+ssh://git@github.com/owner/repo@1.0.0`, `hg+ssh://…`, `svn+ssh://…` — so an SSH remote no
  longer needs a `User` entry in `ssh_config`. It is the SSH login, part of the identity
  (`…git%2Bssh:%2F%2Fgit%40github.com%2F…` in `.project.json` and the lock) and of the address
  the VCS executable is given. A password anywhere, and a user name on any other transport,
  stay refused.
