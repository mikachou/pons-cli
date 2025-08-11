# Pons CLI

## Overview

Pons CLI is a command-line interface for the PONS dictionary API. It allows you to look up translations for words in various languages.

![pons-cli](https://github.com/mikachou/pons-cli/blob/main/pons-cli.png?raw=true)

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
go install github.com/mikachou/pons-cli@latest
```

Ensure that `$HOME/go/bin` is in your `$PATH` environment variable to launch the program.

## Usage

Launch application from terminal using `pons-cli` command:

```
pons-cli
```

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
- `.history`: Show your search history.
- `.cards <dict> <origin> [<days>]`: Enter flashcards mode to practice your vocabulary.

## Configuration

The configuration file is located at `~/.config/pons-cli/config.toml`.

The following variables can be configured:

- `api_key`: Your PONS API key.
- `cache_ttl`: The time-to-live for the cache in seconds. Default is 604800 (7 days).
- `cmd_history_limit`: The maximum number of commands to store in the history. Default is 100.
- `search_history_limit`: The maximum number of search entries to store in the history. Default is 1000.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENCE) file for details.
