/*
 * Scheduled script execution: "rvav: catalog import", daily (or as often as
 * rvav's catalog changes). Run it once by hand after installing.
 */
new RvavCatalogImport().run();
