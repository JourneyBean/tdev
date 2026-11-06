package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"tdev/internal/gitlabrelease"
)

func main() {
	dir := flag.String("dir", "dist", "signed release directory")
	notes := flag.String("notes", "", "release notes file")
	key := flag.String("public-key", "docs/release.pub", "trusted release public key")
	flag.Parse()
	if os.Getenv("CI_COMMIT_REF_PROTECTED") != "true" {
		fmt.Fprintln(os.Stderr, "GitLab publishing requires a protected tag")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := gitlabrelease.Publish(ctx, gitlabrelease.Options{
		APIURL: os.Getenv("CI_API_V4_URL"), ProjectURL: os.Getenv("CI_PROJECT_URL"), ProjectID: os.Getenv("CI_PROJECT_ID"),
		Tag: os.Getenv("CI_COMMIT_TAG"), Token: os.Getenv("CI_JOB_TOKEN"), Directory: *dir, Notes: *notes, TrustedKey: *key,
	}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("GitLab release published")
}
