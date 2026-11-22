/*
 * Business rule: "rvav: order" on Requested Item [sc_req_item].
 * When: after; Insert and Update ticked; Advanced ticked; no condition.
 *
 * Queues the event rvav.order rather than calling rvav here, so that
 * ordering doesn't wait on rvav: the script action "rvav: submit" calls it.
 */
(function executeRule(current, previous) {
  if (!RvavOrder.isRvav(current) || !RvavOrder.ready(current)) {
    return;
  }
  if (current.operation() === 'insert' || current.approval.changes()) {
    gs.eventQueue('rvav.order', current, current.getValue('number'), '');
  }
})(current, previous);
