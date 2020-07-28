package main

import (
    "fmt"
    "flag"
    "strings"
)

func main() {

    svcGit := flag.String("git", "github.com", "git service")
    repoGit := flag.String("repo", "RedHatOfficial/collector-infra", "koffer plugin repo")
    branchGit := flag.String("branch", "master", "git branch")

    flag.Parse()

    gitslice := []string{*svcGit, "/", *repoGit}
    urigit := strings.Join(gitslice, "")

    fmt.Println("  Service: ", *svcGit)
    fmt.Println("     Repo: ", *repoGit)
    fmt.Println("   Branch: ", *branchGit)
    fmt.Println("      URI: ", urigit)
}

