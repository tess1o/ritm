package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
)

// userCmd manages accounts from the command line. It works while the server is running:
// suspensions, password changes and deletions take effect on the user's next request.
func userCmd(args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("user "+sub, flag.ExitOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation (delete)")
	fs.Parse(args)

	ctx := context.Background()
	a, err := newApp(ctx)
	if err != nil {
		return err
	}
	defer a.db.Close()

	if sub == "list" {
		return listUsers(ctx, a)
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ritm user %s NAME", sub)
	}
	name := fs.Arg(0)
	if sub == "add" {
		pw, err := readNewPassword()
		if err != nil {
			return err
		}
		u, err := a.auth.CreateUser(ctx, name, pw)
		if err != nil {
			return err
		}
		fmt.Printf("created user %s (id %d)\n", u.Username, u.ID)
		return nil
	}

	u, err := a.auth.UserByName(ctx, name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	switch sub {
	case "passwd":
		pw, err := readNewPassword()
		if err != nil {
			return err
		}
		if err := a.auth.SetPassword(ctx, u.ID, pw, ""); err != nil {
			return err
		}
		fmt.Printf("password of %s changed; all their sessions were signed out\n", u.Username)
	case "suspend", "unsuspend":
		if err := a.auth.SetSuspended(ctx, u.ID, sub == "suspend"); err != nil {
			return err
		}
		fmt.Printf("%s: %sed\n", u.Username, sub)
	case "delete":
		if !*yes && !confirm(fmt.Sprintf("Delete %s and all their data? This cannot be undone. Type the username to confirm: ", u.Username), u.Username) {
			return errors.New("aborted")
		}
		if err := a.tenants.DeleteUser(ctx, u.User); err != nil {
			return err
		}
		fmt.Printf("deleted %s\n", u.Username)
	default:
		return fmt.Errorf("unknown user command %q\n%s", sub, usage)
	}
	return nil
}

func listUsers(ctx context.Context, a *app) error {
	users, err := a.auth.ListUsers(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tUSERNAME\tCREATED\tSTATUS")
	for _, u := range users {
		status := "active"
		if u.SuspendedAt != nil {
			status = "suspended " + u.SuspendedAt.Format("2006-01-02")
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", u.ID, u.Username, u.CreatedAt.Format("2006-01-02 15:04"), status)
	}
	return tw.Flush()
}

var stdin = bufio.NewReader(os.Stdin)

// readNewPassword prompts twice without echo on a terminal; otherwise it reads one line from
// stdin (e.g. `echo "$PW" | ritm user add bob`).
func readNewPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := stdin.ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no password on stdin")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	read := func(prompt string) (string, error) {
		fmt.Fprint(os.Stderr, prompt)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	pw, err := read("New password: ")
	if err != nil {
		return "", err
	}
	again, err := read("Repeat password: ")
	if err != nil {
		return "", err
	}
	if pw != again {
		return "", errors.New("passwords do not match")
	}
	return pw, nil
}

func confirm(prompt, want string) bool {
	fmt.Fprint(os.Stderr, prompt)
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line) == want
}
