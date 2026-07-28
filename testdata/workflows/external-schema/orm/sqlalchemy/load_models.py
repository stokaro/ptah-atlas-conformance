from atlas_provider_sqlalchemy.ddl import print_ddl

from models import Pet, User


print_ddl("sqlite", [User, Pet])
