// A person picked by searching their name or email (rvav.users).
import { useTranslation } from "react-i18next";
import { SearchBox, type SearchWords } from "./search";
import type { WidgetProps } from "./widgets";

/** A person, found by name or email; the value is their ID. */
export function UserWidget(props: WidgetProps) {
  const { t } = useTranslation("forms");
  const words: SearchWords = {
    list: "people",
    listName: t("people.listName"),
    placeholder: t("people.placeholder"),
    none: t("people.none"),
    found: (count) => t("people.found", { count }),
    lookupFailed: t("people.lookupFailed"),
  };
  return <SearchBox {...props} words={words} />;
}
