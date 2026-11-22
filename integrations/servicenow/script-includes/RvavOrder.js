/*
 * Script Include: RvavOrder (global, not client callable).
 *
 * Submits a requested item of one of rvav's catalog items to rvav, on
 * behalf of its requested_for, saying who placed the order (opened_by):
 * rvav refuses an order for someone else unless that person may request
 * for others in rvav. rvav answers the same request for the same sys_id,
 * so submitting twice is harmless; rvav then writes the progress to the
 * requested item itself.
 */
var RvavOrder = Class.create();

RvavOrder.ITEM_PREFIX = 'rvav:';
RvavOrder.INPUT_PREFIX = 'rvav_in_';
RvavOrder.FIXED = ['rvav_project', 'rvav_environment', 'rvav_deployment_name', 'rvav_justification',
  'rvav_accept_warnings'];
// Pending and Open: the states before rvav's first work note sets 2.
RvavOrder.STATES_WAITING = '-5,1';
RvavOrder.STATE_CLOSED_INCOMPLETE = '4';

RvavOrder.itemIdOf = function (catItem) {
  var meta = String(catItem.getValue('meta') || '');
  return meta.indexOf(RvavOrder.ITEM_PREFIX) === 0 ? meta.substring(RvavOrder.ITEM_PREFIX.length) : '';
};

// A person as rvav knows them: by email, else by user name.
RvavOrder.nameOf = function (ref) {
  if (ref.nil()) {
    return '';
  }
  var user = ref.getRefRecord();
  return String(user.getValue('email') || user.getValue('user_name') || '');
};

RvavOrder.isRvav = function (ritm) {
  return !ritm.cat_item.nil() && RvavOrder.itemIdOf(ritm.cat_item.getRefRecord()) !== '';
};

// ServiceNow's own approval, when the site has one, comes first.
RvavOrder.ready = function (ritm) {
  var approval = String(ritm.getValue('approval') || '');
  if (gs.getProperty('rvav.wait_for_approval', 'false') === 'true') {
    return approval === 'approved';
  }
  return approval === 'approved' || approval === 'not requested' || approval === 'not_required' || approval === '';
};

RvavOrder.prototype = {
  initialize: function (client) {
    this.client = client || new RvavClient();
  },

  // submit answers 'made', 'refused' or 'retry'.
  submit: function (ritm) {
    var draft = this.draft(ritm);
    if (draft === null) {
      return 'refused';
    }
    var res = this.client.submit(draft);
    if (res.status === 200 || res.status === 202) {
      return 'made';
    }
    if (res.status === 0 || res.status === 401 || res.status === 429 || res.status >= 500) {
      gs.warn('rvav: ' + ritm.getValue('number') + ' will be submitted again: ' + RvavClient.problem(res));
      return 'retry';
    }
    this.refuse(ritm, 'rvav refused the request.\n' + RvavClient.problem(res));
    return 'refused';
  },

  draft: function (ritm) {
    var item = ritm.cat_item.getRefRecord();
    var itemId = RvavOrder.itemIdOf(item);
    var who = RvavOrder.nameOf(ritm.requested_for);
    var by = RvavOrder.nameOf(ritm.opened_by);
    if (itemId === '' || who === '' || by === '') {
      this.refuse(ritm, 'rvav: the item, the person it is requested for or the person who ordered it is unknown to rvav.');
      return null;
    }
    var v = ritm.variables;
    var draft = {
      ritm_sys_id: ritm.getUniqueValue(),
      ritm_number: String(ritm.getValue('number')),
      user: who,
      ordered_by: by,
      catalog_item_id: itemId,
      project_id: String(v.rvav_project || ''),
      environment: String(v.rvav_environment || ''),
      inputs: {}
    };
    var name = String(v.rvav_deployment_name || '');
    if (name !== '') {
      draft.deployment_name = name;
    }
    var why = String(v.rvav_justification || '');
    if (why !== '') {
      draft.justification = why.substring(0, 2000);
    }
    if (String(v.rvav_accept_warnings || '') === 'true') {
      draft.accept_warnings = true;
    }
    // rvav takes the strings as the form's types, and an empty one as the
    // form's initial value.
    for (var key in v) {
      if (key.indexOf(RvavOrder.INPUT_PREFIX) === 0) {
        draft.inputs[key.substring(RvavOrder.INPUT_PREFIX.length)] = String(v[key]);
      }
    }
    return draft;
  },

  refuse: function (ritm, note) {
    ritm.work_notes = note;
    ritm.setValue('state', RvavOrder.STATE_CLOSED_INCOMPLETE);
    ritm.update();
  },

  // retry submits again the open requested items rvav has no request for:
  // those rvav couldn't take when they were ordered.
  retry: function () {
    var gr = new GlideRecord('sc_req_item');
    gr.addActiveQuery();
    gr.addQuery('state', 'IN', RvavOrder.STATES_WAITING);
    gr.addQuery('cat_item.meta', 'STARTSWITH', RvavOrder.ITEM_PREFIX);
    gr.addQuery('sys_updated_on', '<', gs.minutesAgo(5));
    gr.addQuery('sys_created_on', '>', gs.daysAgo(3));
    gr.orderBy('sys_created_on');
    gr.setLimit(50);
    gr.query();
    while (gr.next()) {
      if (!RvavOrder.ready(gr)) {
        continue;
      }
      var known = this.client.request(gr.getUniqueValue());
      if (known.status === 200) {
        continue;
      }
      if (known.status !== 404 || this.submit(gr) === 'retry') {
        return;
      }
    }
  },

  type: 'RvavOrder'
};
