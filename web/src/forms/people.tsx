// A person picked by searching their name or email (rvav.users).
import { SearchBox, type SearchWords } from "./search";
import type { WidgetProps } from "./widgets";

const peopleWords: SearchWords = {
  list: "people",
  listName: "people found",
  placeholder: "Name or email",
  none: "Nobody found.",
  found: (n) =>
    n === 1 ? "1 person found: press Enter to pick them." : `${n} people found: use the arrow keys, then Enter.`,
  lookupFailed: "Couldn't look up this person.",
};

/** A person, found by name or email; the value is their ID. */
export function UserWidget(props: WidgetProps) {
  return <SearchBox {...props} words={peopleWords} />;
}
