/*
 * Catalog client script: "rvav: projects", on the variable set rvav.
 * Type onLoad; UI type All; Applies on a Catalog Item view.
 *
 * Offers the projects the user may request the item in.
 */
function onLoad() {
  g_form.clearOptions('rvav_environment');
  var ga = new GlideAjax('RvavAjax');
  ga.addParam('sysparm_name', 'projects');
  ga.addParam('sysparm_item', g_form.getUniqueValue());
  ga.getXMLAnswer(function (answer) {
    var out = JSON.parse(answer || '{}');
    if (out.error) {
      g_form.addErrorMessage(out.error);
      return;
    }
    g_form.clearOptions('rvav_project');
    g_form.addOption('rvav_project', '', '-- None --');
    for (var i = 0; i < out.projects.length; i++) {
      g_form.addOption('rvav_project', out.projects[i].value, out.projects[i].label);
    }
    if (out.projects.length === 0) {
      g_form.addErrorMessage('No project of yours in rvav offers this item.');
    } else if (out.projects.length === 1) {
      g_form.setValue('rvav_project', out.projects[0].value);
    }
  });
}
