package updater

import "strings"

// Restart starts the replaced executable with the current process arguments.
func Restart() error {
	return restartSelf()
}

func restartArgs(args []string) []string {
	result := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--update":
			if index+1 < len(args) && isUpdateSubcommand(args[index+1]) {
				index++
				if args[index] == "branch" && index+1 < len(args) {
					index++
				}
			}
		case strings.HasPrefix(argument, "--update="):
		case argument == "--maintenance" && index+1 < len(args) && args[index+1] == "update":
			index++
			if index+1 < len(args) && isUpdateSubcommand(args[index+1]) {
				index++
				if args[index] == "branch" && index+1 < len(args) {
					index++
				}
			}
		case argument == "--maintenance=update":
			if index+1 < len(args) && isUpdateSubcommand(args[index+1]) {
				index++
				if args[index] == "branch" && index+1 < len(args) {
					index++
				}
			}
		default:
			result = append(result, argument)
		}
	}
	return result
}

func isUpdateSubcommand(value string) bool {
	return value == "check" || value == "yes" || value == "branch"
}
