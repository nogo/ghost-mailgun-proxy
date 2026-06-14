package main

import "strings"

// replaceRecipientVars substitutes %recipient.X% placeholders in content
// with values from the vars map. Unknown keys are left as-is.
func replaceRecipientVars(content string, vars map[string]string) string {
	for key, value := range vars {
		content = strings.ReplaceAll(content, "%recipient."+key+"%", value)
	}
	return content
}

// cleanListUnsubscribe removes the Mailgun-specific <%tag_unsubscribe_email%>
// token and any preceding ", " from a List-Unsubscribe header value.
func cleanListUnsubscribe(header string) string {
	header = strings.ReplaceAll(header, ", <%tag_unsubscribe_email%>", "")
	header = strings.ReplaceAll(header, "<%tag_unsubscribe_email%>", "")
	return strings.TrimSpace(header)
}
