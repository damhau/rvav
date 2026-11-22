/*
 * Script Include: RvavAjax (global, Client callable ticked).
 *
 * The projects and environments the signed-in user may request a catalog
 * item of rvav's in, for the catalog client scripts. The user is always
 * the session's, never a parameter: no one reads another user's
 * entitlements through it.
 */
var RvavAjax = Class.create();

RvavAjax.prototype = Object.extendsObject(AbstractAjaxProcessor, {
  // Answers {projects: [{value, label, environments: [{value, label}]}]},
  // or {error}.
  projects: function () {
    var item = new GlideRecord('sc_cat_item');
    if (!item.get(this.getParameter('sysparm_item'))) {
      return JSON.stringify({ error: 'No such catalog item.' });
    }
    var itemId = RvavOrder.itemIdOf(item);
    var user = new GlideRecord('sys_user');
    user.get(gs.getUserID());
    var who = String(user.getValue('email') || user.getValue('user_name') || '');
    if (itemId === '' || who === '') {
      return JSON.stringify({ error: 'rvav does not offer this item to you.' });
    }
    var res = new RvavClient().entitlements(who);
    if (res.status === 404) {
      return JSON.stringify({ error: 'rvav has no user ' + who + ': sign in to rvav once, or ask its administrators.' });
    }
    // Such as a last sign-in to rvav too old to go by.
    if (res.status === 422) {
      var errors = (res.body && res.body.errors) || [];
      return JSON.stringify({ error: errors.length > 0 ? errors[0].message : RvavClient.problem(res) });
    }
    if (res.status !== 200) {
      return JSON.stringify({ error: 'rvav could not be reached. Try again in a minute.' });
    }
    var out = [];
    var projects = res.body.projects || [];
    for (var i = 0; i < projects.length; i++) {
      var p = projects[i];
      if (p.items.indexOf(itemId) >= 0) {
        out.push({ value: p.project.id, label: p.project.name, environments: p.environments });
      }
    }
    return JSON.stringify({ projects: out });
  },

  type: 'RvavAjax'
});
