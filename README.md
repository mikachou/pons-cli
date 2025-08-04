# Pons CLI

## Overview

Pons CLI is a command-line interface for the PONS dictionary API. It allows you to look up translations for words in various languages.

## Requirements

- Go 1.18 or higher (if installed from source code)
- A PONS API key. You can get one by registering at [https://en.pons.com/open_dict/public_api](https://en.pons.com/open_dict/public_api).

## Install

### Archlinux based systems

Using the `yay` command:

```
yay -S pons-cli
```

### Snap package

Using `snap` command:

```
sudo snap install pons-cli

```

### Source code

To install, use `go install`:

```bash
go install github.com/michaelschuh/pons-cli@latest
```

## Usage

Launch application from terminal using `pons-cli` command:

```
pons-cli
```

(if installed from sourcecode, should be installed in `$HOME/go/bin`, then this folder must be added first in `$PATH` environment variable)

First, you need to set your API key:

```
.set api_key <your_api_key>
```

Then, you can list the available dictionaries:

```
.dict
```

Set the dictionary you want to use:

```
.dict <dictionary_key>
```

Now you can start translating:

```
<word>
```

### Commands

- `.help`: Show the help message.
- `.quit`: Exit the program.
- `.dict`: List available dictionaries.
- `.dict <key>`: Set the current dictionary.
- `.set`: Show current settings.
- `.set <var> <value>`: Set a configuration variable.

## Configuration

The configuration file is located at `~/.config/pons-cli/config.toml`.

The following variables can be configured:

- `api_key`: Your PONS API key.
- `cache_ttl`: The time-to-live for the cache in seconds. Default is 604800 (7 days).
- `cmd_history_limit`: The maximum number of commands to store in the history. Default is 100.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENCE) file for details.
