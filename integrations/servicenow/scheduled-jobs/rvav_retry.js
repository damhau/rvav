/*
 * Scheduled script execution: "rvav: retry", every 5 minutes.
 *
 * Submits again the requested items rvav couldn't take when they were
 * ordered (rvav down, the rate limit, a token being replaced), for three
 * days.
 */
new RvavOrder().retry();
