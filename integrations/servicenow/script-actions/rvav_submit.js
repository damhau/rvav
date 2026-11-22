/*
 * Script action: "rvav: submit", on the event rvav.order (registered on
 * sc_req_item). current is the requested item.
 */
(function () {
  if (!RvavOrder.isRvav(current) || !RvavOrder.ready(current)) {
    return;
  }
  try {
    new RvavOrder().submit(current);
  } catch (e) {
    // A connection alias left unset: the job "rvav: retry" submits it once set.
    gs.error('rvav: ' + current.getValue('number') + ' was not submitted: ' + e);
  }
})();
