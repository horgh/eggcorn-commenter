This is a program to parse email files containing comments and generate HTML.

I have a comment system called [eggcorn](https://github.com/horgh/eggcorn).
When people comment on a page, eggcorn sends an email. This email's body
contains JSON and contains everything I want to know about a comment. Using
this program, I take these emails and generate HTML containing the comments.
Then I use this HTML to display the comments.

I use this system for my blog. One reason I do it this way is because I want
my blog to be a static site. Since I wanted to have email notifications when
people post new comments, I decided to include everything about each comment
in these emails, and use them as the data store as well.
