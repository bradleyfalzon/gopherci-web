package users

type User struct{}

func UserFromGitHubLogin(GitHubID int) *User {

	// SELECT...

	// If not found, INSERT...

	return &User{}
}

func (u *User) SetOAuthToken(token string) error {
	return nil
}
