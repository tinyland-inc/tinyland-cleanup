# Darwin Developer Caches

Darwin developer caches are useful but can become large enough to break local
work and CI runner jobs. The daemon now reports typed cache candidates during
dry-run JSON output without deleting IDE settings or project state.

Review with:

```sh
tinyland-cleanup --once --dry-run --level critical --output json
```

The cache plugin plan includes `targets` for known cache families:

- `jetbrains`: versioned directories under `~/Library/Caches/JetBrains`;
- `playwright`: browser revisions under `~/Library/Caches/ms-playwright`;
- `bazelisk`: downloads under `~/Library/Caches/bazelisk`;
- `pip`: caches under `~/Library/Caches/pip` and `~/.cache/pip`.

Targets include the cache type, name, detected version, path, physical bytes,
active-use evidence, protected status, review action, and reason.

Safety boundaries:

- never remove `~/Library/Application Support/JetBrains`;
- never remove project workspaces;
- never remove keychains, auth databases, editor settings, or extension config;
- protect active JetBrains IDE versions when matching processes are running;
- protect newest Playwright browser revisions per browser family;
- protect the newest Bazelisk cache entries.

Real deletion for these typed targets is opt-in. Leave enforcement disabled
until a dry-run has been reviewed:

```yaml
darwin_dev_caches:
  enabled: true
  enforce: false
```

When `enforce: true`, moderate cleanup can delete unprotected Playwright and
Bazelisk entries outside the keep-latest policy and stale pip caches. Aggressive
cleanup can delete inactive stale JetBrains cache versions. Critical cleanup can
delete inactive unprotected JetBrains cache versions regardless of age. The same
protected target rules from dry-run planning are used for real cleanup.
