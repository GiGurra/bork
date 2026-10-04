use zed_extension_api::{self as zed, settings::LspSettings, LanguageServerId, Worktree};

struct Bork;

impl zed::Extension for Bork {
    fn new() -> Self {
        Self
    }

    fn language_server_command(
        &mut self,
        server: &LanguageServerId,
        worktree: &Worktree,
    ) -> zed::Result<zed::Command> {
        let settings = LspSettings::for_worktree(server.as_ref(), worktree)?.binary;
        let command = settings
            .as_ref()
            .and_then(|binary| binary.path.clone())
            .or_else(|| worktree.which("bork"))
            .ok_or("Install bork on PATH, or set lsp.bork.binary.path in Zed settings")?;
        let args = settings
            .as_ref()
            .and_then(|binary| binary.arguments.clone())
            .unwrap_or_else(|| vec!["lsp".into()]);
        let mut env = worktree.shell_env();
        if let Some(extra) = settings.and_then(|binary| binary.env) {
            for (key, value) in extra {
                env.retain(|(existing, _)| existing != &key);
                env.push((key, value));
            }
        }
        Ok(zed::Command { command, args, env })
    }
}

zed::register_extension!(Bork);
