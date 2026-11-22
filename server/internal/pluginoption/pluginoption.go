// Package pluginoption holds what a plugin's option takes: a declaration
// to the plugin contract, and a value, set or default, to its
// declaration. The plugin host holds a plugin's declarations to it when
// the plugin is loaded, and the repository's reader holds what a file
// sets to it, at the line that set it: one implementation of both, so a
// default and a value set are held alike.
package pluginoption

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// DescriptionAtMost is the most bytes an option's description may hold:
// one sentence.
const DescriptionAtMost = 200

// Declared holds an option's declaration to the contract, the default
// aside: it says what it does in one line, bounds an integer alone, and
// allows words only of a kind of text, each once. What it says is
// named in the error, never what it holds.
func Declared(option *pluginv1.Option) error {
	name, description := option.GetName(), option.GetDescription()
	kind := option.GetKind()
	text := kind == pluginv1.Option_KIND_STRING || kind == pluginv1.Option_KIND_STRING_LIST || kind == pluginv1.Option_KIND_STRING_MAP
	switch {
	case strings.TrimSpace(description) == "":
		return fmt.Errorf("the option %s says nothing of what it does: an option has a description", name)
	case len(description) > DescriptionAtMost || !line(description):
		return fmt.Errorf("the option %s has a description that is not one line of printable text of at most %d bytes", name, DescriptionAtMost)
	case (option.Minimum != nil || option.Maximum != nil) && kind != pluginv1.Option_KIND_INT:
		return fmt.Errorf("the option %s bounds a value that is no integer: only an integer has a least and a most", name)
	case option.Minimum != nil && option.Maximum != nil && option.GetMinimum() > option.GetMaximum():
		return fmt.Errorf("the option %s has a least above its most", name)
	case len(option.GetAllowed()) > 0 && !text:
		return fmt.Errorf("the option %s allows words, and takes no text: only a string, a list or a map of text allows words", name)
	}
	for i, word := range option.GetAllowed() {
		switch {
		case word == "" || !line(word):
			return fmt.Errorf("the option %s allows a word that is not one line of printable text", name)
		case slices.Contains(option.GetAllowed()[:i], word):
			return fmt.Errorf("the option %s allows a word twice", name)
		}
	}
	return nil
}

// Takes holds a value to what an option takes: its kind, its bounds and
// the words it allows. It names the option and the rule, never the
// value, which may hold what is not to be repeated.
func Takes(option *pluginv1.Option, value *typev1.OptionValue) error {
	name := option.GetName()
	switch option.GetKind() {
	case pluginv1.Option_KIND_STRING:
		v, is := value.GetValue().(*typev1.OptionValue_StringValue)
		if !is {
			return fmt.Errorf("%s is text", name)
		}
		return allowed(option, "", v.StringValue)
	case pluginv1.Option_KIND_BOOL:
		if _, is := value.GetValue().(*typev1.OptionValue_BoolValue); !is {
			return fmt.Errorf("%s is true or false", name)
		}
	case pluginv1.Option_KIND_INT:
		v, is := value.GetValue().(*typev1.OptionValue_IntValue)
		switch {
		case !is:
			return fmt.Errorf("%s is a whole number", name)
		case option.Minimum != nil && v.IntValue < option.GetMinimum():
			return fmt.Errorf("%s is at least %d", name, option.GetMinimum())
		case option.Maximum != nil && v.IntValue > option.GetMaximum():
			return fmt.Errorf("%s is at most %d", name, option.GetMaximum())
		}
	case pluginv1.Option_KIND_STRING_LIST:
		v, is := value.GetValue().(*typev1.OptionValue_StringListValue)
		if !is || v.StringListValue == nil {
			return fmt.Errorf("%s is a list of text", name)
		}
		for _, entry := range v.StringListValue.GetValues() {
			if err := allowed(option, "an entry of ", entry); err != nil {
				return err
			}
		}
	case pluginv1.Option_KIND_DURATION:
		v, is := value.GetValue().(*typev1.OptionValue_DurationValue)
		if !is || v.DurationValue == nil || v.DurationValue.CheckValid() != nil || v.DurationValue.AsDuration() < 0 {
			return fmt.Errorf("%s is a length of time, not below zero, as 90s or 10m", name)
		}
	case pluginv1.Option_KIND_STRING_MAP:
		v, is := value.GetValue().(*typev1.OptionValue_StringMapValue)
		if !is || v.StringMapValue == nil {
			return fmt.Errorf("%s is a map of text by name", name)
		}
		for _, key := range slices.Sorted(maps.Keys(v.StringMapValue.GetValues())) {
			if err := allowed(option, "a value of ", v.StringMapValue.GetValues()[key]); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("the plugin does not say what kind of value %s takes", name)
	}
	return nil
}

// allowed holds a word to the words an option allows, if it allows any.
func allowed(option *pluginv1.Option, of, word string) error {
	words := option.GetAllowed()
	if len(words) == 0 || slices.Contains(words, word) {
		return nil
	}
	return fmt.Errorf("%s%s is one of %s", of, option.GetName(), strings.Join(words, ", "))
}

// line reports whether a text is one line of printable characters.
func line(text string) bool {
	if !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
